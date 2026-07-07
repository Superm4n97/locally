package util

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/skip2/go-qrcode"
)

func TestRenderQRHalfBlocks(t *testing.T) {
	bits := [][]bool{
		{true, false},
		{false, true},
		{true, false},
		{true, false},
	}
	out := renderQR(bits, "http://x", true)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	// frame + 2 packed rows + frame + 2 caption lines
	if len(lines) != 6 {
		t.Fatalf("got %d lines, want 6:\n%s", len(lines), out)
	}
	stripANSI := func(s string) string {
		for _, code := range []string{ansiReset, ansiInk, ansiDim, ansiCyan} {
			s = strings.ReplaceAll(s, code, "")
		}
		return s
	}
	if got := stripANSI(lines[1]); got != "│▀▄│" {
		t.Errorf("row 1 = %q, want %q", got, "│▀▄│")
	}
	if got := stripANSI(lines[2]); got != "│█ │" {
		t.Errorf("row 2 = %q, want %q", got, "│█ │")
	}
}

func TestRenderQROddRowsAndInversion(t *testing.T) {
	bits := [][]bool{
		{true, false},
		{false, true},
		{true, false},
	}
	// no color: modules are inverted, and the virtual row below the bitmap
	// is light, so it must render as ink (▄) under the inverted last row
	out := renderQR(bits, "http://x", false)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("got %d lines, want 6:\n%s", len(lines), out)
	}
	if lines[1] != "│▄▀│" {
		t.Errorf("row 1 = %q, want %q", lines[1], "│▄▀│")
	}
	if lines[2] != "│▄█│" {
		t.Errorf("row 2 = %q, want %q", lines[2], "│▄█│")
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("plain rendering must not contain ANSI escape codes")
	}
}

func TestRenderQRRealCodeDimensions(t *testing.T) {
	qr, err := qrcode.New("http://192.168.0.10:8000", qrcode.Low)
	if err != nil {
		t.Fatal(err)
	}
	bits := qr.Bitmap()
	out := renderQR(bits, "http://192.168.0.10:8000", false)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	wantRows := (len(bits)+1)/2 + 2 // packed module rows plus frame
	if len(lines) != wantRows+2 {   // plus caption
		t.Errorf("got %d lines, want %d", len(lines), wantRows+2)
	}
	wantWidth := len(bits[0]) + 2 // frame bars
	for i := 0; i < wantRows; i++ {
		if got := utf8.RuneCountInString(lines[i]); got != wantWidth {
			t.Errorf("line %d width = %d runes, want %d", i, got, wantWidth)
		}
	}
	if !strings.Contains(out, "http://192.168.0.10:8000") {
		t.Error("caption must include the url")
	}
}

func TestRenderQREmptyBitmap(t *testing.T) {
	if out := renderQR(nil, "http://x", true); out != "" {
		t.Errorf("empty bitmap should render nothing, got %q", out)
	}
}
