// Package qrpic renders payloads as QR code images and reads them back.
package qrpic

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"  // register GIF decoder
	_ "image/jpeg" // register JPEG decoder
	"image/png"
	"io"
	"os"
	"strings"

	qrcodegen "github.com/skip2/go-qrcode"
)

// Level is a QR error-correction level.
type Level = qrcodegen.RecoveryLevel

// QuietZone is the number of light modules drawn around every symbol.
const QuietZone = 4

// byteCapacity holds the maximum number of bytes one QR code can store in byte
// mode, for version 40 (177x177 modules) at each error-correction level.
var byteCapacity = map[Level]int{
	qrcodegen.Low:     2953,
	qrcodegen.Medium:  2331,
	qrcodegen.High:    1663, // "Q"
	qrcodegen.Highest: 1273, // "H"
}

// ParseLevel maps a level name ("L", "M", "Q", "H") to a recovery level.
func ParseLevel(name string) (Level, error) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "L":
		return qrcodegen.Low, nil
	case "M":
		return qrcodegen.Medium, nil
	case "Q":
		return qrcodegen.High, nil
	case "H":
		return qrcodegen.Highest, nil
	default:
		return 0, fmt.Errorf("unknown error-correction level %q: use L, M, Q or H", name)
	}
}

// LevelName returns the single-letter name of a level.
func LevelName(level Level) string {
	switch level {
	case qrcodegen.Low:
		return "L"
	case qrcodegen.Medium:
		return "M"
	case qrcodegen.High:
		return "Q"
	case qrcodegen.Highest:
		return "H"
	}
	return "?"
}

// Capacity returns the byte-mode capacity of the largest QR code at level.
func Capacity(level Level) int { return byteCapacity[level] }

// Render encodes payload as a PNG QR code. scale is the side length in pixels
// of one module; a quiet zone of QuietZone modules is added around the symbol.
func Render(payload string, level Level, scale int) ([]byte, error) {
	if scale < 1 {
		return nil, fmt.Errorf("scale must be at least 1 pixel per module, got %d", scale)
	}
	if len(payload) > Capacity(level) {
		return nil, fmt.Errorf("payload of %d bytes does not fit into one QR code at level %s (max %d)",
			len(payload), LevelName(level), Capacity(level))
	}
	q, err := qrcodegen.New(payload, level)
	if err != nil {
		return nil, fmt.Errorf("cannot encode QR code: %w", err)
	}
	q.DisableBorder = false // keep the quiet zone, readers need it
	var buf bytes.Buffer
	if err := png.Encode(&buf, q.Image(-scale)); err != nil {
		return nil, fmt.Errorf("cannot write PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// DecodeImage reads the QR code in r and returns its payload as text.
//
// Two strategies are tried: first the exact module-grid sampler, which is what
// images written by this tool need, then the general-purpose detector, which
// also handles rotated, skewed or photographed codes.
func DecodeImage(r io.Reader) ([]string, error) {
	img, format, err := image.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("cannot read image: %w", err)
	}
	if text, ok := decodeByGrid(img); ok {
		return []string{text}, nil
	}
	text, err := decodeWithDetector(img)
	if err != nil {
		return nil, fmt.Errorf("no QR code found in %s image: %w", format, err)
	}
	return []string{text}, nil
}

// DecodeFile opens path and decodes the QR code it contains.
func DecodeFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	payloads, err := DecodeImage(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return payloads, nil
}
