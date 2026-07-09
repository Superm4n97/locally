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
	"time"

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
	s.renderListing(w, r, fsPath)
}

type breadcrumb struct {
	Name string
	Href string
}

type entry struct {
	Name    string
	Href    string
	IsDir   bool
	Kind    string // "dir", "image", "video", "doc" or "other"
	Size    string
	ModTime string
	modTime time.Time
}

// monthGroup is a run of files sharing the same month and year,
// rendered under a single sticky "January 2026"-style heading.
type monthGroup struct {
	Label   string
	Entries []entry
}

type pageData struct {
	Path        string
	Breadcrumbs []breadcrumb
	Filter      string
	Dirs        []entry
	Groups      []monthGroup
}

// kindByExt classifies files by extension. Covers the formats iPhone
// (HEIC/MOV) and Android (JPG/MP4/3GP) cameras produce, plus common
// document types.
var kindByExt = map[string]string{
	".jpg": "image", ".jpeg": "image", ".png": "image", ".gif": "image",
	".webp": "image", ".bmp": "image", ".svg": "image", ".avif": "image",
	".heic": "image", ".heif": "image", ".tif": "image", ".tiff": "image",

	".mp4": "video", ".mov": "video", ".m4v": "video", ".webm": "video",
	".mkv": "video", ".avi": "video", ".3gp": "video", ".3g2": "video",
	".mts": "video", ".wmv": "video",

	".pdf": "doc", ".doc": "doc", ".docx": "doc", ".xls": "doc",
	".xlsx": "doc", ".ppt": "doc", ".pptx": "doc", ".odt": "doc",
	".ods": "doc", ".odp": "doc", ".txt": "doc", ".md": "doc",
	".csv": "doc", ".rtf": "doc",
}

func fileKind(name string) string {
	if kind, ok := kindByExt[strings.ToLower(filepath.Ext(name))]; ok {
		return kind
	}
	return "other"
}

// filterKinds maps the ?filter= query values to the file kind they keep.
var filterKinds = map[string]string{
	"photos": "image",
	"videos": "video",
	"docs":   "doc",
}

func (s *server) renderListing(w http.ResponseWriter, r *http.Request, fsPath string) {
	filter := r.URL.Query().Get("filter")
	if _, ok := filterKinds[filter]; !ok {
		filter = "all"
	}

	dirEntries, err := os.ReadDir(fsPath)
	if err != nil {
		http.Error(w, "failed to read directory", http.StatusInternalServerError)
		return
	}

	cleaned := path.Clean("/" + r.URL.Path)
	var dirs, files []entry
	for _, de := range dirEntries {
		// Hidden files and directories (dotfiles) are not listed.
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		e := entry{
			Name:  de.Name(),
			Href:  (&url.URL{Path: path.Join(cleaned, de.Name())}).EscapedPath(),
			IsDir: de.IsDir(),
			Kind:  "dir",
			Size:  "—",
		}
		if info, err := de.Info(); err == nil {
			e.modTime = info.ModTime()
			e.ModTime = e.modTime.Format("Jan 02, 2006 15:04")
			if !de.IsDir() {
				e.Size = formatSize(info.Size())
			}
		}
		if de.IsDir() {
			dirs = append(dirs, e)
			continue
		}
		e.Kind = fileKind(de.Name())
		if filter != "all" && e.Kind != filterKinds[filter] {
			continue
		}
		files = append(files, e)
	}

	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	sort.Slice(files, func(i, j int) bool {
		if !files[i].modTime.Equal(files[j].modTime) {
			return files[i].modTime.After(files[j].modTime)
		}
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTmpl.Execute(w, pageData{
		Path:        cleaned,
		Breadcrumbs: breadcrumbsFor(cleaned),
		Filter:      filter,
		Dirs:        dirs,
		Groups:      groupByMonth(files),
	}); err != nil {
		klog.Errorf("failed to render listing for %s: %v", cleaned, err)
	}
}

// groupByMonth splits files (already sorted newest-first) into
// consecutive month-and-year groups.
func groupByMonth(files []entry) []monthGroup {
	var groups []monthGroup
	for _, e := range files {
		label := e.modTime.Format("January 2006")
		if len(groups) == 0 || groups[len(groups)-1].Label != label {
			groups = append(groups, monthGroup{Label: label})
		}
		groups[len(groups)-1].Entries = append(groups[len(groups)-1].Entries, e)
	}
	return groups
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
