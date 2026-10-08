package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// sampleText returns n bytes of deterministic, only moderately compressible
// text, so that an encode run produces several QR codes.
func sampleText(n int) []byte {
	var b strings.Builder
	state := uint32(12345)
	for b.Len() < n {
		state = state*1664525 + 1013904223
		b.WriteString("line ")
		b.WriteString(itoa(int(state % 100000)))
		b.WriteString(": ")
		for i := 0; i < 40; i++ {
			state = state*1664525 + 1013904223
			b.WriteByte(byte('a' + state%26))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String()[:n])
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	content := sampleText(60_000)
	input := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(input, content, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	prefix := filepath.Join(dir, "notes")
	if err := run([]string{"encode", "-o", prefix, input}, &stdout, &stderr); err != nil {
		t.Fatalf("encode: %v (stderr: %s)", err, stderr.String())
	}
	images, err := filepath.Glob(prefix + "-*.png")
	if err != nil {
		t.Fatal(err)
	}
	if len(images) < 2 {
		t.Fatalf("expected a sequence of images, got %d", len(images))
	}

	// Feed the images in reverse order: the chunk index in the payload, not the
	// order on the command line, must decide how they are joined.
	reversed := append([]string(nil), images...)
	sort.Sort(sort.Reverse(sort.StringSlice(reversed)))
	target := filepath.Join(dir, "restored.txt")
	stdout.Reset()
	stderr.Reset()
	args := append([]string{"decode", "-o", target}, reversed...)
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("decode: %v (stderr: %s)", err, stderr.String())
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(content))
	}
}

func TestEncodeDecodeSingleImageToStdout(t *testing.T) {
	dir := t.TempDir()
	content := []byte("just a short note\n")
	input := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(input, content, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"encode", "-o", filepath.Join(dir, "note"), input}, &stdout, &stderr); err != nil {
		t.Fatalf("encode: %v", err)
	}
	image := filepath.Join(dir, "note.png")
	if _, err := os.Stat(image); err != nil {
		t.Fatalf("expected a single image %s: %v", image, err)
	}
	stdout.Reset()
	if err := run([]string{"decode", image}, &stdout, &stderr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(stdout.Bytes(), content) {
		t.Fatalf("got %q, want %q", stdout.String(), content)
	}
}

func TestDecodeDirectoryOfImages(t *testing.T) {
	dir := t.TempDir()
	content := sampleText(20_000)
	input := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(input, content, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"encode", "-o", dir + string(os.PathSeparator), input}, &stdout, &stderr); err != nil {
		t.Fatalf("encode: %v", err)
	}
	target := filepath.Join(t.TempDir(), "out.txt")
	stdout.Reset()
	if err := run([]string{"decode", "-o", target, dir}, &stdout, &stderr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("round trip mismatch when decoding a whole directory")
	}
}

func TestDecodeReportsMissingChunks(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(input, sampleText(20_000), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"encode", "-o", filepath.Join(dir, "data"), input}, &stdout, &stderr); err != nil {
		t.Fatalf("encode: %v", err)
	}
	images, err := filepath.Glob(filepath.Join(dir, "data-*.png"))
	if err != nil || len(images) < 2 {
		t.Fatalf("expected several images, got %d (%v)", len(images), err)
	}
	err = run([]string{"decode", images[1]}, &stdout, &stderr)
	if err == nil {
		t.Fatal("decoding a single chunk of a sequence succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "incomplete sequence") {
		t.Fatalf("got %v, want an incomplete-sequence error", err)
	}
}

func TestEncodeRejectsChunkTooLargeForLevel(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(input, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := run([]string{"encode", "-level", "H", "-chunk", "5000", input}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("got %v, want a chunk-size error", err)
	}
}

func TestEncodeRejectsUnknownLevel(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(input, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"encode", "-level", "Z", input}, &stdout, &stderr); err == nil {
		t.Fatal("encode accepted an unknown level")
	}
}

func TestRunUsageAndVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(stdout.String(), "qrcode") {
		t.Fatalf("unexpected version output %q", stdout.String())
	}
	if err := run(nil, &stdout, &stderr); err == nil {
		t.Fatal("running without a command succeeded, want an error")
	}
	if err := run([]string{"nonsense"}, &stdout, &stderr); err == nil {
		t.Fatal("running an unknown command succeeded, want an error")
	}
}

func TestOutputPathsNumbering(t *testing.T) {
	paths := outputPaths("out", "chunk", 12)
	if len(paths) != 12 {
		t.Fatalf("got %d paths, want 12", len(paths))
	}
	if paths[0] != filepath.Join("out", "chunk-01.png") || paths[11] != filepath.Join("out", "chunk-12.png") {
		t.Fatalf("unexpected names: %v ... %v", paths[0], paths[11])
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	for i := range sorted {
		if sorted[i] != paths[i] {
			t.Fatalf("names do not sort into chunk order: %v", paths)
		}
	}
	if single := outputPaths("out", "chunk", 1); single[0] != filepath.Join("out", "chunk.png") {
		t.Fatalf("single image should keep the plain name, got %v", single[0])
	}
}
