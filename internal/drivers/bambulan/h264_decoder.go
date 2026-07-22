package bambulan

import (
	"image"

	"github.com/polimero-app/cli/internal/h264decode"
)

// h264FrameDecoder wraps the shared h264decode.Decoder for use in the
// bambulan driver's snapshot pipeline.
type h264FrameDecoder struct {
	dec *h264decode.Decoder
}

func newH264FrameDecoder() (*h264FrameDecoder, error) {
	dec, err := h264decode.New()
	if err != nil {
		return nil, err
	}
	return &h264FrameDecoder{dec: dec}, nil
}

func (d *h264FrameDecoder) close() {
	d.dec.Close()
}

func (d *h264FrameDecoder) decode(au [][]byte) (*image.RGBA, error) {
	return d.dec.Decode(au)
}
