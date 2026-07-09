package expose

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestServer creates a server for a temp directory seeded with:
//
//	hello.txt      ("hello world")
//	sub/nested.txt ("nested content")
//
// The thumbnail cache is isolated per test and ffmpeg is disabled so
// results do not depend on what is installed on the machine.
func newTestServer(t *testing.T) (*server, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "nested.txt"), []byte("nested content"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := newServer(root)
	if err != nil {
		t.Fatalf("newServer(%q) failed: %v", root, err)
	}
	s.thumbDir = t.TempDir()
	s.ffmpeg, s.ffprobe = "", ""
	return s, root
}

func newTestHandler(t *testing.T) (http.Handler, string) {
	t.Helper()
	s, root := newTestServer(t)
	return s.routes(), root
}

func multipartBody(t *testing.T, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for filename, content := range fields {
		fw, err := w.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func doUpload(t *testing.T, h http.Handler, dir, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := multipartBody(t, map[string]string{filename: content})
	req := httptest.NewRequest(http.MethodPost, "/api/upload?dir="+dir, body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNewHandlerRejectsNonDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewHandler(file); err == nil {
		t.Error("expected error for non-directory root, got nil")
	}
	if _, err := NewHandler(filepath.Join(root, "missing")); err == nil {
		t.Error("expected error for missing root, got nil")
	}
}

func TestBrowseListsDirectory(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"hello.txt", "sub"} {
		if !strings.Contains(body, want) {
			t.Errorf("listing missing %q", want)
		}
	}
}

func TestBrowseSubdirectory(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "nested.txt") {
		t.Error("subdirectory listing missing nested.txt")
	}
}

func TestDownloadFile(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hello.txt", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "hello world" {
		t.Errorf("body = %q, want %q", got, "hello world")
	}
}

func TestBrowseMissingPathReturns404(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no-such-file", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestBrowsePathTraversalBlocked(t *testing.T) {
	h, root := newTestHandler(t)
	secret := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(secret)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path = "/../secret.txt"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "top secret") {
		t.Error("path traversal escaped the served root")
	}
}

func TestUploadCreatesFile(t *testing.T) {
	h, root := newTestHandler(t)
	rec := doUpload(t, h, "/", "upload.txt", "uploaded data")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body)
	}
	var res struct {
		Saved []string `json:"saved"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if len(res.Saved) != 1 || res.Saved[0] != "upload.txt" {
		t.Errorf("saved = %v, want [upload.txt]", res.Saved)
	}
	got, err := os.ReadFile(filepath.Join(root, "upload.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "uploaded data" {
		t.Errorf("file content = %q, want %q", got, "uploaded data")
	}
}

func TestUploadToSubdirectory(t *testing.T) {
	h, root := newTestHandler(t)
	rec := doUpload(t, h, "/sub", "upload.txt", "sub data")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "upload.txt")); err != nil {
		t.Errorf("uploaded file not found in subdirectory: %v", err)
	}
}

func TestUploadMultipleFiles(t *testing.T) {
	h, root := newTestHandler(t)
	body, contentType := multipartBody(t, map[string]string{
		"a.txt": "aaa",
		"b.txt": "bbb",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/upload?dir=/", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("uploaded file %s not found: %v", name, err)
		}
	}
}

func TestUploadSanitizesFilename(t *testing.T) {
	h, root := newTestHandler(t)
	rec := doUpload(t, h, "/", "../../evil.txt", "evil")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "evil.txt")); err != nil {
		t.Errorf("sanitized upload not found in root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.txt")); err == nil {
		t.Error("upload escaped the served root")
	}
}

func TestUploadCollisionGetsUniqueName(t *testing.T) {
	h, root := newTestHandler(t)
	rec := doUpload(t, h, "/", "hello.txt", "second")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "hello.txt (1)")); err == nil {
		t.Error("collision suffix was appended after the extension")
	}
	got, err := os.ReadFile(filepath.Join(root, "hello (1).txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Errorf("collision file content = %q, want %q", got, "second")
	}
	original, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != "hello world" {
		t.Errorf("original file was overwritten: %q", original)
	}
}

func TestUploadToMissingDirReturns404(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := doUpload(t, h, "/no-such-dir", "upload.txt", "data")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestUploadRejectsNonPost(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/upload", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestUploadMissingFileFieldReturns400(t *testing.T) {
	h, _ := newTestHandler(t)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("note", "no file here"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/upload?dir=/", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain.txt", "plain.txt"},
		{"../../etc/passwd", "passwd"},
		{`C:\Users\me\doc.pdf`, "doc.pdf"},
		{"dir/sub/file.txt", "file.txt"},
		{"..", ""},
		{".", ""},
		{"", ""},
		{"/", ""},
	}
	for _, c := range cases {
		if got := sanitizeFilename(c.in); got != c.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFileKind(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"IMG_0001.JPG", "image"},
		{"photo.jpeg", "image"},
		{"shot.png", "image"},
		{"iphone.HEIC", "image"},
		{"clip.mp4", "video"},
		{"iphone.MOV", "video"},
		{"android.3gp", "video"},
		{"report.pdf", "doc"},
		{"letter.docx", "doc"},
		{"sheet.xlsx", "doc"},
		{"notes.txt", "doc"},
		{"archive.zip", "other"},
		{"binary", "other"},
	}
	for _, c := range cases {
		if got := fileKind(c.name); got != c.want {
			t.Errorf("fileKind(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// seedMedia writes a mix of media files into root with distinct mod times.
func seedMedia(t *testing.T, root string) {
	t.Helper()
	files := map[string]time.Time{
		"photo.jpg": time.Date(2026, time.March, 15, 10, 0, 0, 0, time.Local),
		"video.mp4": time.Date(2026, time.March, 2, 9, 0, 0, 0, time.Local),
		"doc.pdf":   time.Date(2025, time.December, 25, 8, 0, 0, 0, time.Local),
		"blob.bin":  time.Date(2025, time.December, 1, 7, 0, 0, 0, time.Local),
	}
	for name, mt := range files {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFilterPhotos(t *testing.T) {
	h, root := newTestHandler(t)
	seedMedia(t, root)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?filter=photos", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "photo.jpg") {
		t.Error("photos filter dropped photo.jpg")
	}
	for _, unwanted := range []string{"video.mp4", "doc.pdf", "blob.bin", "hello.txt"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("photos filter kept %q", unwanted)
		}
	}
	if !strings.Contains(body, "sub") {
		t.Error("photos filter hid directories; they must stay navigable")
	}
}

func TestFilterVideosAndDocs(t *testing.T) {
	h, root := newTestHandler(t)
	seedMedia(t, root)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?filter=videos", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "video.mp4") || strings.Contains(body, "photo.jpg") {
		t.Error("videos filter returned wrong entries")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?filter=docs", nil))
	body = rec.Body.String()
	if !strings.Contains(body, "doc.pdf") || !strings.Contains(body, "hello.txt") {
		t.Error("docs filter dropped document files")
	}
	if strings.Contains(body, "photo.jpg") || strings.Contains(body, "blob.bin") {
		t.Error("docs filter kept non-document files")
	}
}

func TestInvalidFilterFallsBackToAll(t *testing.T) {
	h, root := newTestHandler(t)
	seedMedia(t, root)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?filter=bogus", nil))

	body := rec.Body.String()
	for _, want := range []string{"photo.jpg", "video.mp4", "doc.pdf", "blob.bin"} {
		if !strings.Contains(body, want) {
			t.Errorf("invalid filter should show everything, missing %q", want)
		}
	}
}

func TestListingSortedNewestFirstWithMonthHeaders(t *testing.T) {
	h, root := newTestHandler(t)
	seedMedia(t, root)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	march := strings.Index(body, "March 2026")
	december := strings.Index(body, "December 2025")
	if march == -1 || december == -1 {
		t.Fatalf("month headers missing: march=%d december=%d", march, december)
	}
	if march > december {
		t.Error("March 2026 must appear before December 2025 (newest first)")
	}
	photo := strings.Index(body, "photo.jpg")
	video := strings.Index(body, "video.mp4")
	if photo == -1 || video == -1 || photo > video {
		t.Error("within a month, newer files must come first")
	}
}

func TestGroupByMonth(t *testing.T) {
	mk := func(y int, m time.Month, d int) entry {
		return entry{modTime: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
	}
	groups := groupByMonth([]entry{
		mk(2026, time.March, 20),
		mk(2026, time.March, 1),
		mk(2026, time.January, 5),
		mk(2025, time.December, 31),
	})
	wantLabels := []string{"March 2026", "January 2026", "December 2025"}
	if len(groups) != len(wantLabels) {
		t.Fatalf("got %d groups, want %d", len(groups), len(wantLabels))
	}
	for i, want := range wantLabels {
		if groups[i].Label != want {
			t.Errorf("group[%d].Label = %q, want %q", i, groups[i].Label, want)
		}
	}
	if len(groups[0].Entries) != 2 {
		t.Errorf("March 2026 group has %d entries, want 2", len(groups[0].Entries))
	}

	if got := groupByMonth(nil); got != nil {
		t.Errorf("groupByMonth(nil) = %v, want nil", got)
	}
}

func TestHiddenFilesNotListed(t *testing.T) {
	h, root := newTestHandler(t)
	if err := os.WriteFile(filepath.Join(root, ".hidden.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	for _, unwanted := range []string{".hidden.txt", ".config"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("listing shows hidden entry %q", unwanted)
		}
	}
	if !strings.Contains(body, "hello.txt") {
		t.Error("listing dropped visible file hello.txt")
	}
}

func TestMediaRendersInlinePreviews(t *testing.T) {
	h, root := newTestHandler(t)
	seedMedia(t, root)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `src="/api/thumb?path=%2Fphoto.jpg"`) {
		t.Error("image entry missing thumbnail <img> pointing at /api/thumb")
	}
	// Without ffmpeg, videos fall back to a lazily loaded <video> element.
	if !strings.Contains(body, `<video preload="none" data-src="/video.mp4"`) {
		t.Error("video entry missing lazy <video> preview")
	}
}

func TestVideoTileUsesPosterWhenFFmpegPresent(t *testing.T) {
	s, root := newTestServer(t)
	s.ffmpeg = "/usr/bin/ffmpeg" // only affects template output; nothing is executed
	seedMedia(t, root)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `src="/api/thumb?path=%2Fvideo.mp4"`) {
		t.Error("video entry should use a static poster <img> when ffmpeg is available")
	}
	if strings.Contains(body, "<video") {
		t.Error("no <video> elements expected when posters are available")
	}
}

// writePNG writes a w x h PNG image to path.
func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
}

func getThumb(t *testing.T, h http.Handler, urlPath string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/thumb?path="+url.QueryEscape(urlPath), nil))
	return rec
}

func TestThumbDownscalesImage(t *testing.T) {
	s, root := newTestServer(t)
	writePNG(t, filepath.Join(root, "big.png"), 800, 600)
	rec := getThumb(t, s.routes(), "/big.png")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=86400") {
		t.Errorf("thumb Cache-Control = %q, want long max-age", cc)
	}
	img, err := jpeg.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("thumbnail is not a valid JPEG: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 320 || got.Dy() != 240 {
		t.Errorf("thumbnail size = %dx%d, want 320x240", got.Dx(), got.Dy())
	}
}

func TestThumbIsCachedOnDisk(t *testing.T) {
	s, root := newTestServer(t)
	writePNG(t, filepath.Join(root, "big.png"), 800, 600)
	h := s.routes()

	if rec := getThumb(t, h, "/big.png"); rec.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, want 200", rec.Code)
	}
	cache, err := os.ReadDir(s.thumbDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cache) != 1 {
		t.Fatalf("thumb cache has %d entries, want 1", len(cache))
	}
	first, err := os.Stat(filepath.Join(s.thumbDir, cache[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if rec := getThumb(t, h, "/big.png"); rec.Code != http.StatusOK {
		t.Fatalf("second request: status = %d, want 200", rec.Code)
	}
	second, err := os.Stat(filepath.Join(s.thumbDir, cache[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !second.ModTime().Equal(first.ModTime()) {
		t.Error("cached thumbnail was regenerated on the second request")
	}
}

func TestThumbFallsBackToOriginalOnDecodeFailure(t *testing.T) {
	s, root := newTestServer(t)
	// A .jpg that is not actually a JPEG (stands in for HEIC and friends).
	if err := os.WriteFile(filepath.Join(root, "fake.jpg"), []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := getThumb(t, s.routes(), "/fake.jpg")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "not an image" {
		t.Error("expected the original file bytes as fallback")
	}
}

func TestThumbMissingFileReturns404(t *testing.T) {
	h, _ := newTestHandler(t)
	if rec := getThumb(t, h, "/nope.jpg"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestThumbVideoWithoutFFmpegReturns404(t *testing.T) {
	s, root := newTestServer(t)
	seedMedia(t, root)
	if rec := getThumb(t, s.routes(), "/video.mp4"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when ffmpeg is unavailable", rec.Code)
	}
}

func TestThumbSize(t *testing.T) {
	cases := []struct {
		w, h, wantW, wantH int
	}{
		{800, 600, 320, 240},
		{600, 800, 240, 320},
		{100, 50, 100, 50}, // never upscale
		{320, 320, 320, 320},
		{4000, 10, 320, 1},
	}
	for _, c := range cases {
		if w, h := thumbSize(c.w, c.h); w != c.wantW || h != c.wantH {
			t.Errorf("thumbSize(%d, %d) = %dx%d, want %dx%d", c.w, c.h, w, h, c.wantW, c.wantH)
		}
	}
}

func TestPagination(t *testing.T) {
	h, root := newTestHandler(t)
	base := time.Now().Add(-time.Hour)
	for i := 1; i <= 120; i++ {
		name := fmt.Sprintf("img%03d.jpg", i)
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(-time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?filter=photos", nil))
	body := rec.Body.String()
	for _, want := range []string{"img001.jpg", "img100.jpg", "sub"} {
		if !strings.Contains(body, want) {
			t.Errorf("page 1 missing %q", want)
		}
	}
	if strings.Contains(body, "img101.jpg") {
		t.Error("page 1 leaked entries beyond the page size")
	}
	if !strings.Contains(body, `href="?filter=photos&amp;page=2"`) {
		t.Error("page 1 missing link to page 2")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?filter=photos&page=2", nil))
	body = rec.Body.String()
	for _, want := range []string{"img101.jpg", "img120.jpg"} {
		if !strings.Contains(body, want) {
			t.Errorf("page 2 missing %q", want)
		}
	}
	if strings.Contains(body, "img001.jpg") {
		t.Error("page 2 shows entries from page 1")
	}
	if strings.Contains(body, "📁 sub/") {
		t.Error("directories must only be listed on page 1")
	}
	if !strings.Contains(body, `href="?filter=photos"`) {
		t.Error("page 2 missing link back to page 1")
	}
}

func TestPaginationOutOfRangeFallsBackToFirstPage(t *testing.T) {
	h, root := newTestHandler(t)
	seedMedia(t, root)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?page=99", nil))

	if !strings.Contains(rec.Body.String(), "photo.jpg") {
		t.Error("out-of-range page should fall back to the first page")
	}
}

func TestCacheHeaders(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hello.txt", nil))
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=3600") {
		t.Errorf("file Cache-Control = %q, want max-age=3600", cc)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("listing Cache-Control = %q, want no-cache", cc)
	}
}

// TestThumbVideoWithFFmpeg exercises the real ffmpeg poster path.
// Skipped when ffmpeg is not installed on the machine.
func TestThumbVideoWithFFmpeg(t *testing.T) {
	s, root := newTestServer(t)
	real, err := newServer(root)
	if err != nil {
		t.Fatal(err)
	}
	if real.ffmpeg == "" {
		t.Skip("ffmpeg not installed")
	}
	s.ffmpeg, s.ffprobe = real.ffmpeg, real.ffprobe

	// Generate a tiny test clip with ffmpeg itself.
	clip := filepath.Join(root, "clip.mp4")
	if out, err := exec.Command(s.ffmpeg, "-v", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=10",
		"-y", clip).CombinedOutput(); err != nil {
		t.Fatalf("generating test clip: %v: %s", err, out)
	}

	rec := getThumb(t, s.routes(), "/clip.mp4")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body)
	}
	if _, err := jpeg.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
		t.Errorf("video poster is not a valid JPEG: %v", err)
	}
}

func TestFormatSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1 << 20, "1.0 MiB"},
		{1 << 30, "1.0 GiB"},
	}
	for _, c := range cases {
		if got := formatSize(c.in); got != c.want {
			t.Errorf("formatSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
