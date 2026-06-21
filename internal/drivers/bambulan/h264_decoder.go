package bambulan

import (
	"fmt"
	"image"
	"runtime"
	"sync"
	"unsafe"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
)

// #cgo pkg-config: libavcodec libavutil libswscale
// #include <errno.h>
// #include <libavcodec/avcodec.h>
// #include <libavutil/imgutils.h>
// #include <libavutil/log.h>
// #include <libswscale/swscale.h>
import "C"

func frameData(frame *C.AVFrame) **C.uint8_t {
	return (**C.uint8_t)(unsafe.Pointer(&frame.data[0]))
}

func frameLineSize(frame *C.AVFrame) *C.int {
	return (*C.int)(unsafe.Pointer(&frame.linesize[0]))
}

type h264FrameDecoder struct {
	codecCtx     *C.AVCodecContext
	yuv420Frame  *C.AVFrame
	rgbaFrame    *C.AVFrame
	rgbaFramePtr []uint8
	swsCtx       *C.struct_SwsContext
}

var quietFFmpegLogs sync.Once

func newH264FrameDecoder() (*h264FrameDecoder, error) {
	quietFFmpegLogs.Do(func() {
		C.av_log_set_level(C.AV_LOG_QUIET)
	})

	codec := C.avcodec_find_decoder(C.AV_CODEC_ID_H264)
	if codec == nil {
		return nil, fmt.Errorf("H.264 decoder not found")
	}

	d := &h264FrameDecoder{}
	d.codecCtx = C.avcodec_alloc_context3(codec)
	if d.codecCtx == nil {
		return nil, fmt.Errorf("H.264 decoder allocation failed")
	}

	res := C.avcodec_open2(d.codecCtx, codec, nil)
	if res < 0 {
		C.avcodec_free_context(&d.codecCtx)
		return nil, fmt.Errorf("H.264 decoder open failed")
	}

	d.yuv420Frame = C.av_frame_alloc()
	if d.yuv420Frame == nil {
		C.avcodec_free_context(&d.codecCtx)
		return nil, fmt.Errorf("H.264 frame allocation failed")
	}

	return d, nil
}

func (d *h264FrameDecoder) close() {
	if d.swsCtx != nil {
		C.sws_freeContext(d.swsCtx)
	}
	if d.rgbaFrame != nil {
		C.av_frame_free(&d.rgbaFrame)
	}
	if d.yuv420Frame != nil {
		C.av_frame_free(&d.yuv420Frame)
	}
	if d.codecCtx != nil {
		C.avcodec_free_context(&d.codecCtx)
	}
}

func (d *h264FrameDecoder) reinitRGBAFrame() error {
	if d.swsCtx != nil {
		C.sws_freeContext(d.swsCtx)
	}
	if d.rgbaFrame != nil {
		C.av_frame_free(&d.rgbaFrame)
	}

	d.rgbaFrame = C.av_frame_alloc()
	if d.rgbaFrame == nil {
		return fmt.Errorf("RGBA frame allocation failed")
	}

	d.rgbaFrame.format = C.AV_PIX_FMT_RGBA
	d.rgbaFrame.width = d.yuv420Frame.width
	d.rgbaFrame.height = d.yuv420Frame.height
	d.rgbaFrame.color_range = C.AVCOL_RANGE_JPEG

	res := C.av_frame_get_buffer(d.rgbaFrame, 1)
	if res < 0 {
		return fmt.Errorf("RGBA frame buffer allocation failed")
	}

	d.swsCtx = C.sws_getContext(
		d.yuv420Frame.width,
		d.yuv420Frame.height,
		int32(d.yuv420Frame.format),
		d.rgbaFrame.width,
		d.rgbaFrame.height,
		int32(d.rgbaFrame.format),
		C.SWS_BILINEAR,
		nil,
		nil,
		nil,
	)
	if d.swsCtx == nil {
		return fmt.Errorf("RGBA conversion setup failed")
	}

	rgbaFrameSize := C.av_image_get_buffer_size(int32(d.rgbaFrame.format), d.rgbaFrame.width, d.rgbaFrame.height, 1)
	d.rgbaFramePtr = (*[1 << 30]uint8)(unsafe.Pointer(d.rgbaFrame.data[0]))[:rgbaFrameSize:rgbaFrameSize]
	return nil
}

// containsH264Slice reports whether au contains an actual picture slice
// (IDR or non-IDR), as opposed to only parameter sets or SEI data.
func containsH264Slice(au [][]byte) bool {
	for _, nalu := range au {
		if len(nalu) == 0 {
			continue
		}
		switch h264.NALUType(nalu[0] & 0x1F) {
		case h264.NALUTypeIDR, h264.NALUTypeNonIDR:
			return true
		}
	}
	return false
}

func (d *h264FrameDecoder) decode(au [][]byte) (*image.RGBA, error) {
	annexb, err := h264.AnnexB(au).Marshal()
	if err != nil {
		return nil, err
	}
	if len(annexb) == 0 {
		return nil, nil
	}

	var pkt C.AVPacket
	ptr := &annexb[0]
	var pinner runtime.Pinner
	pinner.Pin(ptr)
	pkt.data = (*C.uint8_t)(ptr)
	pkt.size = C.int(len(annexb))
	res := C.avcodec_send_packet(d.codecCtx, &pkt)
	pinner.Unpin()
	if res < 0 {
		if !containsH264Slice(au) {
			// No picture data in this access unit (parameter sets and/or
			// SEI only); there is nothing for the decoder to reject, so
			// this is not a real decode failure.
			return nil, nil
		}
		return nil, fmt.Errorf("H.264 decoder rejected access unit (libavcodec error %d)", int(res))
	}

	res = C.avcodec_receive_frame(d.codecCtx, d.yuv420Frame)
	if res < 0 {
		if res == -C.EAGAIN {
			return nil, nil
		}
		return nil, fmt.Errorf("H.264 decoder failed to produce frame (libavcodec error %d)", int(res))
	}

	if d.rgbaFrame == nil || d.rgbaFrame.width != d.yuv420Frame.width || d.rgbaFrame.height != d.yuv420Frame.height {
		if err := d.reinitRGBAFrame(); err != nil {
			return nil, err
		}
	}

	res = C.sws_scale(
		d.swsCtx,
		frameData(d.yuv420Frame),
		frameLineSize(d.yuv420Frame),
		0,
		d.yuv420Frame.height,
		frameData(d.rgbaFrame),
		frameLineSize(d.rgbaFrame),
	)
	if res < 0 {
		return nil, fmt.Errorf("RGBA conversion failed")
	}

	return &image.RGBA{
		Pix:    d.rgbaFramePtr,
		Stride: 4 * int(d.rgbaFrame.width),
		Rect: image.Rectangle{
			Max: image.Point{X: int(d.rgbaFrame.width), Y: int(d.rgbaFrame.height)},
		},
	}, nil
}
