package h264decode

import (
	"image"
	"testing"
)

// Fixture: minimal H.264 elementary stream (16x16 black IDR, libx264 baseline).
var testSPS = []byte{0x67, 0x42, 0xc0, 0x0a, 0xd9, 0x1e, 0xc0, 0x44, 0x00, 0x00, 0x03, 0x00, 0x04, 0x00, 0x00, 0x03, 0x00, 0xc8, 0x3c, 0x48, 0x99, 0x20}
var testPPS = []byte{0x68, 0xcb, 0x83, 0xcb, 0x20}
var testIDR = []byte{0x65, 0x88, 0x84, 0x0a, 0xf2, 0x62, 0x80, 0x00, 0xa7, 0xbe}

func TestDecoder_DecodesValidKeyframe(t *testing.T) {
	dec, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer dec.Close()

	img, err := dec.Decode([][]byte{testSPS, testPPS, testIDR})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if img == nil {
		t.Fatal("expected decoded image, got nil")
	}
	if img.Bounds() != image.Rect(0, 0, 16, 16) {
		t.Fatalf("bounds = %v, want 16x16", img.Bounds())
	}
}

func TestDecoder_ParameterSetsOnly_ReturnsNil(t *testing.T) {
	dec, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer dec.Close()

	img, err := dec.Decode([][]byte{testSPS, testPPS})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if img != nil {
		t.Fatal("expected nil image for parameter-sets-only AU")
	}
}

func TestDecoder_CorruptedSlice_ReturnsError(t *testing.T) {
	dec, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer dec.Close()

	garbage := []byte{0x65, 0xff, 0x00, 0xde, 0xad, 0xbe, 0xef, 0x13, 0x37, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	_, err = dec.Decode([][]byte{testSPS, testPPS, garbage})
	if err == nil {
		t.Fatal("expected error for corrupted slice")
	}
}

func TestDecoder_EmptyAU_ReturnsNil(t *testing.T) {
	dec, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer dec.Close()

	img, err := dec.Decode([][]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if img != nil {
		t.Fatal("expected nil for empty AU")
	}
}

func TestContainsSlice(t *testing.T) {
	cases := []struct {
		name string
		au   [][]byte
		want bool
	}{
		{"IDR", [][]byte{testSPS, testPPS, testIDR}, true},
		{"non-IDR", [][]byte{{0x41, 0x01, 0x02}}, true},
		{"SPS+PPS only", [][]byte{testSPS, testPPS}, false},
		{"SEI only", [][]byte{{0x06, 0x01, 0x02}}, false},
		{"empty", [][]byte{}, false},
		{"nil NALU", [][]byte{nil, {}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ContainsSlice(c.au); got != c.want {
				t.Errorf("ContainsSlice = %v, want %v", got, c.want)
			}
		})
	}
}
