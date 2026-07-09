package expose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/image/draw"

	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// thumbMaxDim is the longest edge of a generated thumbnail, sized for the
// ~140px grid tiles (2x for high-DPI phone screens).
const thumbMaxDim = 320

// defaultThumbDir returns a per-user cache directory for generated
// thumbnails, falling back to the system temp directory.
func defaultThumbDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "locally", "thumbs")
	}
	return filepath.Join(os.TempDir(), "locally-thumbs")
}

// thumbKey names the cache file for a source file. Path, mod time and size
// are all part of the key, so an edited file gets a fresh thumbnail.
func thumbKey(fsPath string, info os.FileInfo) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%d|%d", fsPath, info.ModTime().UnixNano(), info.Size()))
	return hex.EncodeToString(sum[:16])
}

// handleThumb serves a small JPEG preview of the file named by ?path=.
// Generated thumbnails are cached on disk. If a thumbnail cannot be
// produced for an image (unsupported format, corrupt file), the original
// file is served so the browser can still try to render it.
func (s *server) handleThumb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	fsPath := s.resolve(r.URL.Query().Get("path"))
	info, err := os.Stat(fsPath)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	cached := filepath.Join(s.thumbDir, thumbKey(fsPath, info)+".jpg")
	if _, err := os.Stat(cached); err != nil {
		if err := s.generateThumb(fsPath, cached); err != nil {
			if fileKind(fsPath) == "image" {
				w.Header().Set("Cache-Control", "public, max-age=3600")
				http.ServeFile(w, r, fsPath)
				return
			}
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, cached)
}

// generateThumb writes a thumbnail for src to dst, limiting how many
// generations run at once so a page full of tiles cannot fork-bomb the
// host or exhaust memory decoding large photos.
func (s *server) generateThumb(src, dst string) error {
	s.thumbSem <- struct{}{}
	defer func() { <-s.thumbSem }()

	// Another request may have generated it while we waited.
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	switch fileKind(src) {
	case "image":
		return s.imageThumb(src, dst)
	case "video":
		if s.ffmpeg == "" {
			return fmt.Errorf("ffmpeg not available for video thumbnail")
		}
		return s.videoThumb(src, dst)
	}
	return fmt.Errorf("no thumbnail for %s", src)
}

// imageThumb decodes src (JPEG/PNG/GIF/WebP/BMP/TIFF) and writes a
// downscaled JPEG to dst atomically.
func (s *server) imageThumb(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	b := img.Bounds()
	w, h := thumbSize(b.Dx(), b.Dy())
	scaled := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), img, b, draw.Src, nil)

	tmp, err := os.CreateTemp(s.thumbDir, "tmp-*.jpg")
	if err != nil {
		return err
	}
	if err := jpeg.Encode(tmp, scaled, &jpeg.Options{Quality: 75}); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// thumbSize fits w x h inside thumbMaxDim, never upscaling.
func thumbSize(w, h int) (int, int) {
	if w <= thumbMaxDim && h <= thumbMaxDim {
		return w, h
	}
	if w >= h {
		return thumbMaxDim, max(1, h*thumbMaxDim/w)
	}
	return max(1, w*thumbMaxDim/h), thumbMaxDim
}

// videoThumb extracts a single frame from the middle of the video with
// ffmpeg and writes it to dst atomically.
func (s *server) videoThumb(src, dst string) error {
	seek := "0"
	if d := s.videoDuration(src); d > 0 {
		seek = strconv.FormatFloat(d/2, 'f', 3, 64)
	}
	tmp := dst + ".tmp.jpg"
	cmd := exec.Command(s.ffmpeg,
		"-v", "error",
		"-ss", seek,
		"-i", src,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:-2", thumbMaxDim),
		"-q:v", "5",
		"-y", tmp,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return os.Rename(tmp, dst)
}

// videoDuration returns the duration of src in seconds via ffprobe,
// or 0 if it cannot be determined.
func (s *server) videoDuration(src string) float64 {
	if s.ffprobe == "" {
		return 0
	}
	out, err := exec.Command(s.ffprobe,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		src,
	).Output()
	if err != nil {
		return 0
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return d
}
