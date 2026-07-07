package expose

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/klog/v2"
)

//go:embed ui/index.html
var indexHTML string

var indexTmpl = template.Must(template.New("index").Parse(indexHTML))

// maxUploadMemory is how much of a multipart body is kept in memory;
// anything beyond it is spooled to temporary files by net/http.
const maxUploadMemory = 32 << 20

type server struct {
	root string
}

// NewHandler returns a handler that serves a browsable listing of root,
// serves its files for download, and accepts uploads on POST /api/upload.
func NewHandler(root string) (http.Handler, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}

	s := &server{root: abs}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/", s.handleBrowse)
	return mux, nil
}

// resolve maps a URL path to a filesystem path inside the served root.
// Cleaning the rooted path first guarantees the result cannot escape root.
func (s *server) resolve(urlPath string) string {
	cleaned := path.Clean("/" + urlPath)
	return filepath.Join(s.root, filepath.FromSlash(cleaned))
}

func (s *server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	fsPath := s.resolve(r.URL.Path)
	info, err := os.Stat(fsPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !info.IsDir() {
		http.ServeFile(w, r, fsPath)
		return
	}
	s.renderListing(w, r.URL.Path, fsPath)
}

type breadcrumb struct {
	Name string
	Href string
}

type entry struct {
	Name    string
	Href    string
	IsDir   bool
	Size    string
	ModTime string
}

type pageData struct {
	Path        string
	Breadcrumbs []breadcrumb
	Entries     []entry
}

func (s *server) renderListing(w http.ResponseWriter, urlPath, fsPath string) {
	dirEntries, err := os.ReadDir(fsPath)
	if err != nil {
		http.Error(w, "failed to read directory", http.StatusInternalServerError)
		return
	}

	cleaned := path.Clean("/" + urlPath)
	entries := make([]entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		e := entry{
			Name:  de.Name(),
			Href:  (&url.URL{Path: path.Join(cleaned, de.Name())}).EscapedPath(),
			IsDir: de.IsDir(),
			Size:  "—",
		}
		if info, err := de.Info(); err == nil {
			e.ModTime = info.ModTime().Format("Jan 02, 2006 15:04")
			if !de.IsDir() {
				e.Size = formatSize(info.Size())
			}
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTmpl.Execute(w, pageData{
		Path:        cleaned,
		Breadcrumbs: breadcrumbsFor(cleaned),
		Entries:     entries,
	}); err != nil {
		klog.Errorf("failed to render listing for %s: %v", cleaned, err)
	}
}

func breadcrumbsFor(cleanedPath string) []breadcrumb {
	crumbs := []breadcrumb{{Name: "home", Href: "/"}}
	trimmed := strings.Trim(cleanedPath, "/")
	if trimmed == "" {
		return crumbs
	}
	href := ""
	for _, seg := range strings.Split(trimmed, "/") {
		href += "/" + url.PathEscape(seg)
		crumbs = append(crumbs, breadcrumb{Name: seg, Href: href})
	}
	return crumbs
}

type uploadResult struct {
	Saved []string `json:"saved"`
}

func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	dir := s.resolve(r.URL.Query().Get("dir"))
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		http.Error(w, "upload directory not found", http.StatusNotFound)
		return
	}
	if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
		http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		http.Error(w, `missing form field "file"`, http.StatusBadRequest)
		return
	}

	saved := make([]string, 0, len(files))
	for _, fh := range files {
		name, err := saveFile(dir, fh)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		klog.Infof("uploaded %s (%s) to %s", name, formatSize(fh.Size), dir)
		saved = append(saved, name)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(uploadResult{Saved: saved}); err != nil {
		klog.Errorf("failed to write upload response: %v", err)
	}
}

func saveFile(dir string, fh *multipart.FileHeader) (string, error) {
	name := sanitizeFilename(fh.Filename)
	if name == "" {
		return "", fmt.Errorf("invalid file name %q", fh.Filename)
	}
	src, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	dst, name, err := createUnique(dir, name)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(dst.Name())
		return "", err
	}
	return name, nil
}

// sanitizeFilename strips any client-supplied directory components so the
// upload can only land directly inside the target directory.
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	if name == "." || name == ".." || name == "/" {
		return ""
	}
	return name
}

// createUnique opens a new file named name inside dir, appending " (N)"
// before the extension on collisions. O_EXCL makes the check race-free.
func createUnique(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	candidate := name
	for i := 1; ; i++ {
		f, err := os.OpenFile(filepath.Join(dir, candidate), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, candidate, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
		if i > 10000 {
			return nil, "", fmt.Errorf("too many files named %q in %s", name, dir)
		}
		candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
	}
}

func formatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
