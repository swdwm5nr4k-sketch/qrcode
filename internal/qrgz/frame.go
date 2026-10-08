// Package qrgz defines the payload format used to move a gzip stream through
// a sequence of QR codes. It knows nothing about images: it turns bytes into
// ordered ASCII payloads and back again.
package qrgz

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// Magic marks a payload produced by this tool. It is followed by the
	// format version, so "GZQR1:" prefixes every payload of format 1.
	Magic = "GZQR"
	// FormatVersion is the payload format revision implemented here.
	FormatVersion = 1
	// prefix is the complete tag that starts every payload.
	// It is the magic plus the format version, i.e. "GZQR1".
	prefix = Magic + "1"

	// maxCountDigits is the width of the widest index/total field, used when
	// computing how much data still fits into one QR code.
	maxCountDigits = 5
)

// encoding is the byte-to-text encoding used for the payload body. It is
// standard base64, URL alphabet, without padding, so that a payload contains
// no characters that a QR code reader, a shell or a file name could mangle.
var encoding = base64.RawURLEncoding

// frameOverhead is the length of the longest possible payload prefix, i.e.
// "GZQR1:99999/99999:".
const frameOverhead = len(prefix) + 1 + 2*maxCountDigits + 1 + 1

// Frame is one chunk of a compressed stream, as carried by one QR code.
type Frame struct {
	// Index is the 1-based position of this chunk in the sequence.
	Index int
	// Total is the number of chunks the sequence contains.
	Total int
	// Data holds the raw (compressed) bytes of this chunk.
	Data []byte
}

// Encode splits data into consecutive chunks of at most chunkSize bytes and
// renders each chunk as a QR payload. The returned payloads are in order; an
// empty input still produces a single (empty) payload so that decoding a
// zero-byte file round-trips.
func Encode(data []byte, chunkSize int) ([]string, error) {
	if chunkSize <= 0 {
		return nil, fmt.Errorf("chunk size must be positive, got %d", chunkSize)
	}
	total := (len(data) + chunkSize - 1) / chunkSize
	if total == 0 {
		total = 1
	}
	if total > 99999 {
		return nil, fmt.Errorf("input needs %d chunks, more than the format allows", total)
	}
	payloads := make([]string, 0, total)
	for i := 0; i < total; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		payloads = append(payloads, Format(i+1, total, data[start:end]))
	}
	return payloads, nil
}

// Format renders one chunk as a payload string.
func Format(index, total int, data []byte) string {
	return fmt.Sprintf("%s:%d/%d:%s", prefix, index, total, encoding.EncodeToString(data))
}

// Parse reads a payload string produced by Format.
func Parse(payload string) (*Frame, error) {
	parts := strings.SplitN(strings.TrimSpace(payload), ":", 3)
	if len(parts) != 3 || parts[0] != prefix {
		return nil, fmt.Errorf("payload does not start with %q: %s", prefix+":", snippet(payload))
	}
	counts := strings.SplitN(parts[1], "/", 2)
	if len(counts) != 2 {
		return nil, fmt.Errorf("malformed chunk counter %q", parts[1])
	}
	index, err := strconv.Atoi(counts[0])
	if err != nil {
		return nil, fmt.Errorf("malformed chunk index %q", counts[0])
	}
	total, err := strconv.Atoi(counts[1])
	if err != nil {
		return nil, fmt.Errorf("malformed chunk total %q", counts[1])
	}
	if total < 1 || total > 99999 {
		return nil, fmt.Errorf("chunk total %d out of range", total)
	}
	if index < 1 || index > total {
		return nil, fmt.Errorf("chunk index %d out of range 1..%d", index, total)
	}
	data, err := encoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("chunk %d/%d is not valid base64: %w", index, total, err)
	}
	return &Frame{Index: index, Total: total, Data: data}, nil
}

// Reassemble sorts frames by index, checks that the sequence is complete and
// returns the concatenated data. Frames may arrive in any order and the same
// frame may be supplied more than once, as long as duplicates are identical.
func Reassemble(frames []*Frame) ([]byte, error) {
	if len(frames) == 0 {
		return nil, errors.New("no QR codes given")
	}
	total := frames[0].Total
	ordered := make([]*Frame, total)
	for _, f := range frames {
		if f.Total != total {
			return nil, fmt.Errorf("QR codes come from different sequences: chunk %d/%d and chunk %d/%d", f.Index, f.Total, frames[0].Index, total)
		}
		slot := &ordered[f.Index-1]
		switch {
		case *slot == nil:
			*slot = f
		case bytes.Equal((*slot).Data, f.Data):
			// Identical duplicate, nothing to do.
		default:
			return nil, fmt.Errorf("chunk %d/%d was given twice with different content", f.Index, total)
		}
	}
	var missing []string
	var buf bytes.Buffer
	for i, f := range ordered {
		if f == nil {
			missing = append(missing, strconv.Itoa(i+1))
			continue
		}
		buf.Write(f.Data)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("incomplete sequence: %d of %d QR codes given, missing chunk(s) %s",
			total-len(missing), total, listOf(missing))
	}
	return buf.Bytes(), nil
}

// MaxChunkSize returns the largest chunk of raw bytes whose payload still fits
// into a QR code with the given byte-mode capacity.
func MaxChunkSize(capacity int) int {
	for n := capacity * 3 / 4; n > 0; n-- {
		if frameOverhead+encoding.EncodedLen(n) <= capacity {
			return n
		}
	}
	return 0
}

// PayloadLen returns the length of the payload that Format produces for a
// chunk of n bytes, assuming the widest possible index/total fields.
func PayloadLen(n int) int {
	return frameOverhead + encoding.EncodedLen(n)
}

// listOf joins a few items and summarises the rest, so that an error about a
// long sequence stays readable.
func listOf(items []string) string {
	const max = 10
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:max], ", ") + fmt.Sprintf(", ... (%d more)", len(items)-max)
}

func snippet(s string) string {
	const max = 40
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return strconv.Quote(s)
	}
	return strconv.Quote(s[:max]) + "..."
}
