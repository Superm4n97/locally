package util

import (
	"fmt"
	"os"
	"strings"

	"github.com/skip2/go-qrcode"
)

const (
	ansiReset = "\x1b[0m"
	ansiInk   = "\x1b[30;47m" // black ink on white paper: scannable on any terminal theme
	ansiDim   = "\x1b[2m"
	ansiCyan  = "\x1b[1;36m"
)

// PrintQRCode renders url as a compact QR code in the terminal, framed and
// captioned with the url itself.
func PrintQRCode(url string) error {
	qr, err := qrcode.New(url, qrcode.Low)
	if err != nil {
		return fmt.Errorf("failed to generate qr code: %w", err)
	}
	fmt.Print(renderQR(qr.Bitmap(), url, colorEnabled()))
	return nil
}

func colorEnabled() bool {
	_, noColor := os.LookupEnv("NO_COLOR")
	return !noColor
}

// renderQR draws the QR bitmap using half-height block characters, packing
// two module rows into each terminal row so the code comes out compact and
// roughly square. With color enabled the modules are forced black-on-white;
// without it the image is inverted, which keeps it scannable on the dark
// terminal themes the plain output is most likely to be viewed on.
func renderQR(bits [][]bool, url string, color bool) string {
	if len(bits) == 0 || len(bits[0]) == 0 {
		return ""
	}
	width := len(bits[0])

	ink, frame, accent, reset := "", "", "", ""
	if color {
		ink, frame, accent, reset = ansiInk, ansiDim, ansiCyan, ansiReset
	}

	dark := func(y, x int) bool {
		d := false
		if y < len(bits) {
			d = bits[y][x]
		}
		if !color {
			d = !d
		}
		return d
	}

	var b strings.Builder
	b.WriteString(frame + "╭" + strings.Repeat("─", width) + "╮" + reset + "\n")
	for y := 0; y < len(bits); y += 2 {
		b.WriteString(frame + "│" + reset + ink)
		for x := 0; x < width; x++ {
			top, bottom := dark(y, x), dark(y+1, x)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString(reset + frame + "│" + reset + "\n")
	}
	b.WriteString(frame + "╰" + strings.Repeat("─", width) + "╯" + reset + "\n")
	b.WriteString("  📱 Scan with your phone camera\n")
	b.WriteString("  🔗 " + accent + url + reset + "\n")
	return b.String()
}
