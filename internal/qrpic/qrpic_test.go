package qrpic

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	qrcodegen "github.com/skip2/go-qrcode"
)

func TestParseLevel(t *testing.T) {
	for name, want := range map[string]Level{
		"L": qrcodegen.Low, "l": qrcodegen.Low,
		"M": qrcodegen.Medium, " m ": qrcodegen.Medium,
		"Q": qrcodegen.High,
		"H": qrcodegen.Highest,
	} {
		got, err := ParseLevel(name)
		if err != nil {
			t.Fatalf("ParseLevel(%q): %v", name, err)
		}
		if got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", name, got, want)
		}
	}
	if _, err := ParseLevel("X"); err == nil {
		t.Fatal("ParseLevel(\"X\") succeeded, want an error")
	}
}

func TestLevelNameRoundTrip(t *testing.T) {
	for _, name := range []string{"L", "M", "Q", "H"} {
		level, err := ParseLevel(name)
		if err != nil {
			t.Fatalf("ParseLevel: %v", err)
		}
		if got := LevelName(level); got != name {
			t.Errorf("LevelName(%v) = %q, want %q", level, got, name)
		}
	}
}

func TestCapacityShrinksWithMoreRecovery(t *testing.T) {
	previous := 1 << 30
	for _, name := range []string{"L", "M", "Q", "H"} {
		level, _ := ParseLevel(name)
		capacity := Capacity(level)
		if capacity >= previous {
			t.Errorf("level %s has capacity %d, expected less than the previous level's %d", name, capacity, previous)
		}
		previous = capacity
	}
}

// TestRenderDecodeRoundTrip is the core contract: whatever we draw, we can read
// back, for every error-correction level and payload size up to the largest
// symbol this tool produces.
func TestRenderDecodeRoundTrip(t *testing.T) {
	payloads := map[string]string{
		"tiny":   "GZQR1:1/1:aGk",
		"medium": repeatPayload(600),
		"dense":  repeatPayload(1500),
	}
	for _, levelName := range []string{"L", "M", "Q", "H"} {
		level, err := ParseLevel(levelName)
		if err != nil {
			t.Fatal(err)
		}
		for name, payload := range payloads {
			if len(payload) > Capacity(level) {
				continue
			}
			t.Run(levelName+"/"+name, func(t *testing.T) {
				png, err := Render(payload, level, 4)
				if err != nil {
					t.Fatalf("Render: %v", err)
				}
				texts, err := DecodeImage(bytes.NewReader(png))
				if err != nil {
					t.Fatalf("DecodeImage: %v", err)
				}
				if len(texts) != 1 || texts[0] != payload {
					t.Fatalf("decoded %d payload(s), first mismatch", len(texts))
				}
			})
		}
	}
}

func TestRenderRejectsOversizedPayload(t *testing.T) {
	level, _ := ParseLevel("H")
	if _, err := Render(repeatPayload(Capacity(level)+1), level, 4); err == nil {
		t.Fatal("Render accepted a payload larger than the symbol capacity")
	}
}

func TestRenderRejectsBadScale(t *testing.T) {
	level, _ := ParseLevel("M")
	if _, err := Render("GZQR1:1/1:aGk", level, 0); err == nil {
		t.Fatal("Render accepted scale 0")
	}
}

// TestDecodeResizedImage covers the grid sampler's tolerance for images that
// have been scaled: the module size is then no longer an integer.
func TestDecodeResizedImage(t *testing.T) {
	level, _ := ParseLevel("M")
	payload := repeatPayload(900)
	encoded, err := Render(payload, level, 8)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	small := scaleImage(img, img.Bounds().Dx()/3)
	var buf bytes.Buffer
	if err := png.Encode(&buf, small); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	texts, err := DecodeImage(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("DecodeImage after resize: %v", err)
	}
	if texts[0] != payload {
		t.Fatal("payload mismatch after resize")
	}
}

// TestDecodeForeignQRCode makes sure the reader is not limited to our own
// payloads: any QR code can be read, it simply may not parse as a frame.
func TestDecodeForeignQRCode(t *testing.T) {
	const want = "https://example.com/hello"
	q, err := qrcodegen.New(want, qrcodegen.Medium)
	if err != nil {
		t.Fatalf("qrcode.New: %v", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, q.Image(-6)); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	texts, err := DecodeImage(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("DecodeImage: %v", err)
	}
	if texts[0] != want {
		t.Fatalf("got %q, want %q", texts[0], want)
	}
}

func TestDecodeImageWithoutQRCode(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 80, 80))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	if _, err := DecodeImage(bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("DecodeImage found a QR code in a blank image")
	}
}

func TestDecodeImageRejectsGarbage(t *testing.T) {
	if _, err := DecodeImage(strings.NewReader("not an image")); err == nil {
		t.Fatal("DecodeImage accepted a non-image")
	}
}

// repeatPayload builds a payload of exactly n bytes that looks like one this
// tool writes.
func repeatPayload(n int) string {
	const head = "GZQR1:1/1:"
	body := strings.Repeat("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", n)
	if n <= len(head) {
		return head[:n]
	}
	return head + body[:n-len(head)]
}

// scaleImage resizes img to size x size with nearest-neighbour sampling, which
// is what a screenshot or an image converter would roughly do.
func scaleImage(img image.Image, size int) image.Image {
	bounds := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			sx := bounds.Min.X + x*bounds.Dx()/size
			sy := bounds.Min.Y + y*bounds.Dy()/size
			out.Set(x, y, img.At(sx, sy))
		}
	}
	return out
}
