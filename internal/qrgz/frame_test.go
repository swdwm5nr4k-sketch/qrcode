package qrgz

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func TestFormatParseRoundTrip(t *testing.T) {
	cases := []struct {
		name         string
		data         []byte
		index, total int
	}{
		{"empty", []byte{}, 1, 1},
		{"utf8", []byte("hello 世界"), 3, 7},
		{"binary", []byte{0, 1, 2, 254, 255}, 12, 99},
		{"big", bytes.Repeat([]byte("abc"), 500), 99999, 99999},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := Format(tc.index, tc.total, tc.data)
			want := Magic + strconv.Itoa(FormatVersion) + ":"
			if !strings.HasPrefix(payload, want) {
				t.Fatalf("payload %q does not start with %q", payload, want)
			}
			frame, err := Parse(payload)
			if err != nil {
				t.Fatalf("Parse(%q): %v", payload, err)
			}
			if frame.Index != tc.index || frame.Total != tc.total {
				t.Errorf("got %d/%d, want %d/%d", frame.Index, frame.Total, tc.index, tc.total)
			}
			if !bytes.Equal(frame.Data, tc.data) {
				t.Errorf("data mismatch: got %d bytes, want %d", len(frame.Data), len(tc.data))
			}
		})
	}
}

func TestParseToleratesSurroundingSpace(t *testing.T) {
	payload := "  " + Format(1, 1, []byte("x")) + "\n"
	if _, err := Parse(payload); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func TestParseRejectsBadPayloads(t *testing.T) {
	cases := map[string]string{
		"foreign":       "https://example.com",
		"wrong version": "GZQR2:1/1:aGk",
		"no counters":   "GZQR1:1:aGk",
		"bad index":     "GZQR1:x/1:aGk",
		"bad total":     "GZQR1:1/x:aGk",
		"zero total":    "GZQR1:1/0:aGk",
		"index too big": "GZQR1:2/1:aGk",
		"bad base64":    "GZQR1:1/1:!!!!",
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(payload); err == nil {
				t.Fatalf("Parse(%q) succeeded, want an error", payload)
			}
		})
	}
}

func TestEncodeSplitsInOrder(t *testing.T) {
	data := make([]byte, 2500)
	for i := range data {
		data[i] = byte(i % 251)
	}
	payloads, err := Encode(data, 1000)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(payloads) != 3 {
		t.Fatalf("got %d payloads, want 3", len(payloads))
	}
	var frames []*Frame
	for _, payload := range payloads {
		frame, err := Parse(payload)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		frames = append(frames, frame)
	}
	if frames[0].Index != 1 || frames[1].Index != 2 || frames[2].Index != 3 {
		t.Errorf("frames are not in order: %d %d %d", frames[0].Index, frames[1].Index, frames[2].Index)
	}
	for i, want := range []int{1000, 1000, 500} {
		if len(frames[i].Data) != want {
			t.Errorf("chunk %d has %d bytes, want %d", i+1, len(frames[i].Data), want)
		}
	}
	got, err := Reassemble(frames)
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("reassembled data differs from the input")
	}
}

func TestEncodeEmptyInputYieldsOnePayload(t *testing.T) {
	payloads, err := Encode(nil, 100)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("got %d payloads, want 1", len(payloads))
	}
	frame, err := Parse(payloads[0])
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(frame.Data) != 0 {
		t.Fatalf("got %d bytes, want 0", len(frame.Data))
	}
}

func TestEncodeRejectsBadChunkSize(t *testing.T) {
	if _, err := Encode([]byte("x"), 0); err == nil {
		t.Fatal("Encode with chunk size 0 succeeded, want an error")
	}
}

func TestReassembleAcceptsAnyOrderAndIdenticalDuplicates(t *testing.T) {
	chunks := [][]byte{[]byte("one "), []byte("two "), []byte("three")}
	frames := []*Frame{
		{Index: 3, Total: 3, Data: chunks[2]},
		{Index: 1, Total: 3, Data: chunks[0]},
		{Index: 2, Total: 3, Data: chunks[1]},
		{Index: 1, Total: 3, Data: chunks[0]},
	}
	got, err := Reassemble(frames)
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	if string(got) != "one two three" {
		t.Fatalf("got %q", got)
	}
}

func TestReassembleErrors(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if _, err := Reassemble(nil); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("missing chunk", func(t *testing.T) {
		frames := []*Frame{{Index: 1, Total: 3, Data: []byte("a")}}
		_, err := Reassemble(frames)
		if err == nil || !strings.Contains(err.Error(), "missing chunk") {
			t.Fatalf("got %v, want a missing-chunk error", err)
		}
	})
	t.Run("conflicting duplicate", func(t *testing.T) {
		frames := []*Frame{
			{Index: 1, Total: 1, Data: []byte("a")},
			{Index: 1, Total: 1, Data: []byte("b")},
		}
		_, err := Reassemble(frames)
		if err == nil || !strings.Contains(err.Error(), "twice") {
			t.Fatalf("got %v, want a conflicting-duplicate error", err)
		}
	})
	t.Run("mixed sequences", func(t *testing.T) {
		frames := []*Frame{
			{Index: 1, Total: 2, Data: []byte("a")},
			{Index: 2, Total: 5, Data: []byte("b")},
		}
		if _, err := Reassemble(frames); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestMaxChunkSizeFitsCapacity(t *testing.T) {
	for _, capacity := range []int{2953, 2331, 1663, 1273} {
		size := MaxChunkSize(capacity)
		if size <= 0 {
			t.Fatalf("capacity %d: got chunk size %d", capacity, size)
		}
		if got := PayloadLen(size); got > capacity {
			t.Errorf("capacity %d: payload of %d bytes does not fit (max %d)", capacity, got, capacity)
		}
		if got := PayloadLen(size + 1); got <= capacity {
			t.Errorf("capacity %d: chunk size %d would fit too, max chunk too small", capacity, size+1)
		}
	}
}
