package qrgz

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompressDecompressRoundTrip(t *testing.T) {
	cases := map[string][]byte{
		"empty":      {},
		"short text": []byte("hello, world\n"),
		"utf8":       []byte(strings.Repeat("二维码测试 ", 100)),
		"binary":     bytes.Repeat([]byte{0, 1, 2, 3, 254, 255}, 1000),
		"repetitive": bytes.Repeat([]byte("the same line over and over\n"), 5000),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			compressed, err := Compress(data)
			if err != nil {
				t.Fatalf("Compress: %v", err)
			}
			got, err := Decompress(compressed)
			if err != nil {
				t.Fatalf("Decompress: %v", err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(data))
			}
		})
	}
}

func TestCompressShrinksRepetitiveText(t *testing.T) {
	data := bytes.Repeat([]byte("compressible text "), 1000)
	compressed, err := Compress(data)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if len(compressed) >= len(data)/2 {
		t.Fatalf("gzip did not shrink the data: %d -> %d", len(data), len(compressed))
	}
}

func TestDecompressRejectsGarbage(t *testing.T) {
	if _, err := Decompress([]byte("this is not gzip data")); err == nil {
		t.Fatal("Decompress of plain text succeeded, want an error")
	}
}

func TestDecompressRejectsTruncatedStream(t *testing.T) {
	compressed, err := Compress(bytes.Repeat([]byte("data"), 100))
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if _, err := Decompress(compressed[:len(compressed)/2]); err == nil {
		t.Fatal("Decompress of a truncated stream succeeded, want an error")
	}
}
