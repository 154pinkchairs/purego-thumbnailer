package thumbnailer

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

// Thumbnail generates a thumbnail from a representative frame of the media.
// Images count as one frame media.
func Thumbnail(dims Dims, inputFile string) (thumb image.Image, err error) {
	buf := bytes.NewBuffer(nil)
	err = ffmpeg.Input(inputFile).
		Filter("select", ffmpeg.Args{fmt.Sprintf("eq(n,%d)", 0)}).
		Output("pipe:", ffmpeg.KwArgs{"vframes": 1, "format": "image2", "vcodec": "mjpeg"}).
		WithOutput(buf, os.Stdout).
		Run()
	if err != nil {
		return nil, err
	}

	thumb, _, err = image.Decode(buf)
	return thumb, err
}

func processMedia(rs io.ReadSeeker, src *Source, opts Options,
) (
	thumb image.Image, err error,
) {
	c, err := newFFContextWithFormat(rs, inputFormats[src.Mime])
	if err != nil {
		return
	}
	defer c.Close()

	src.Length = c.Length()
	src.Meta = c.Meta()
	src.HasAudio, err = c.HasStream(FFAudio)
	if err != nil {
		return
	}
	src.HasVideo, err = c.HasStream(FFVideo)
	if err != nil {
		return
	}
	if src.HasVideo {
		src.Dims, err = c.Dims()
		if err != nil {
			return
		}
	}

	if c.HasCoverArt() {
		thumb, err = processCoverArt(c.CoverArt(), opts)
		switch err {
		case nil:
			return
		case ErrCantThumbnail:
			// Try again on the container itself, if cover art thumbnailing
			// fails
		default:
			return
		}
	}

	if src.HasVideo {
		max := opts.MaxSourceDims
		if max.Width != 0 && src.Width > max.Width {
			err = ErrTooWide
			return
		}
		if max.Height != 0 && src.Height > max.Height {
			err = ErrTooTall
			return
		}
		src.Codec, err = c.CodecName(FFVideo)
		if err != nil {
			return
		}
		thumb, err = c.Thumbnail(opts.ThumbDims)
	} else {
		err = ErrCantThumbnail
	}
	return
}
