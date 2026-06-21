package bambulan

import (
	"image"
	"testing"
)

// Fixture bytes are a real, minimal H.264 elementary stream: a single 16x16
// black IDR frame encoded with libx264 baseline profile. Generated via:
//   ffmpeg -f lavfi -i color=c=black:s=16x16:d=0.04 -frames:v 1 \
//     -c:v libx264 -profile:v baseline -pix_fmt yuv420p -f h264 test.h264
// and extracting the SPS/PPS/IDR NALUs from the Annex-B output.
var testH264SPS = []byte{0x67, 0x42, 0xc0, 0x0a, 0xd9, 0x1e, 0xc0, 0x44, 0x00, 0x00, 0x03, 0x00, 0x04, 0x00, 0x00, 0x03, 0x00, 0xc8, 0x3c, 0x48, 0x99, 0x20}
var testH264PPS = []byte{0x68, 0xcb, 0x83, 0xcb, 0x20}
var testH264IDR = []byte{0x65, 0x88, 0x84, 0x0a, 0xf2, 0x62, 0x80, 0x00, 0xa7, 0xbe}

func TestH264FrameDecoder_DecodesValidKeyframe(t *testing.T) {
	dec, err := newH264FrameDecoder()
	if err != nil {
		t.Fatalf("newH264FrameDecoder: %v", err)
	}
	defer dec.close()

	img, err := dec.decode([][]byte{testH264SPS, testH264PPS, testH264IDR})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if img == nil {
		t.Fatal("expected a decoded image on the first call for a single IDR frame")
	}
	if img.Bounds() != image.Rect(0, 0, 16, 16) {
		t.Fatalf("image bounds = %v, want 16x16", img.Bounds())
	}
}

func TestH264FrameDecoder_CorruptedSliceReturnsError(t *testing.T) {
	dec, err := newH264FrameDecoder()
	if err != nil {
		t.Fatalf("newH264FrameDecoder: %v", err)
	}
	defer dec.close()

	garbageIDR := []byte{0x65, 0xff, 0x00, 0xde, 0xad, 0xbe, 0xef, 0x13, 0x37, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	_, err = dec.decode([][]byte{testH264SPS, testH264PPS, garbageIDR})
	if err == nil {
		t.Fatal("expected an error for a corrupted slice, got nil")
	}
}

func TestH264FrameDecoder_ParameterSetsOnlyNoSliceIsBenign(t *testing.T) {
	dec, err := newH264FrameDecoder()
	if err != nil {
		t.Fatalf("newH264FrameDecoder: %v", err)
	}
	defer dec.close()

	img, err := dec.decode([][]byte{testH264SPS, testH264PPS})
	if err != nil {
		t.Fatalf("expected no error for parameter-sets-only input, got %v", err)
	}
	if img != nil {
		t.Fatal("expected no image for parameter-sets-only input")
	}
}
