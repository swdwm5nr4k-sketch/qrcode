package qrgz

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// MaxDecompressedBytes caps how much data a single decode run will produce, so
// that a hostile QR code cannot exhaust memory.
const MaxDecompressedBytes = 1 << 30 // 1 GiB

// Compress gzips data with the best compression level.
func Compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	return buf.Bytes(), nil
}

// Decompress is the inverse of Compress.
func Decompress(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reassembled data is not a gzip stream: %w", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, MaxDecompressedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("gzip stream is corrupt: %w", err)
	}
	if len(out) > MaxDecompressedBytes {
		return nil, fmt.Errorf("decompressed data exceeds the %d byte limit", MaxDecompressedBytes)
	}
	return out, nil
}
