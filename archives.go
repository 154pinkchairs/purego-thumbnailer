package thumbnailer

import (
	"archive/zip"
	"bytes"
	"image"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/nwaples/rardecode"
)

const (
	mimeZip  = "application/zip"
	mime7Zip = "application/x-7z-compressed"
	mimeRar  = "application/x-rar-compressed"
)

// Thumbnail the first image of a zip file
func processZip(rs io.ReadSeeker, src *Source, opts Options,
) (thumb image.Image, err error) {
	// Obtain io.ReaderAt and find out the size of the file
	var (
		size int64
		ra   io.ReaderAt
	)

	useFile := func(f *os.File) (err error) {
		info, err := f.Stat()
		if err != nil {
			return
		}
		size = info.Size()
		ra = f
		return
	}

	switch rs := rs.(type) {
	case *os.File:
		err = useFile(rs)
		if err != nil {
			return
		}
	case *bytes.Reader:
		r := rs
		ra = r
		size = r.Size()
	default:
		// Dump exotic io.ReadSeeker to file and use that
		var tmp *os.File
		tmp, err = os.CreateTemp("", "")
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()

		_, err = io.Copy(tmp, rs)
		if err != nil {
			return nil, err
		}
		err = useFile(tmp)
		if err != nil {
			return nil, err
		}
	}

	r, err := zip.NewReader(ra, size)
	if err != nil {
		return nil, err
	}

	var (
		imageCount = 0
		firstImage *zip.File
	)
	// Only check the first 10 files. We don't need to check them all.
	for i := 0; i < 10 && i < len(r.File); i++ {
		f := r.File[i]
		if couldBeImage(f.Name) {
			if firstImage == nil {
				firstImage = f
			}
			imageCount++
		}
	}

	// If at least 90% of the first 10 files in the archive root are images,
	// this is a comic archive
	if float32(imageCount)/float32(len(r.File)) >= 0.9 {
		src.Mime = "application/vnd.comicbook+zip"
		src.Extension = "cbz"
	}

	if firstImage == nil {
		err = ErrCantThumbnail
		return
	}

	f, err := firstImage.Open()
	if err != nil {
		return
	}
	defer f.Close()
	thumb, err = thumbnailArchiveImage(f, opts, size*4)
	return
}

// Returns, if file could be an image file, based on it's extension
func couldBeImage(name string) bool {
	if len(name) < 4 {
		return false
	}
	name = strings.ToLower(name[len(name)-4:])
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// Thumbnail image in from an archive
func thumbnailArchiveImage(r io.Reader, opts Options, sizeLimit int64) (thumb image.Image, err error) {
	// Accept anything we can process
	opts.AcceptedMimeTypes = nil

	// Compressed files do not provide seeking.
	// Temporary file to conserve RAM.
	tmp, err := os.CreateTemp("", "")
	if err != nil {
		return nil, err
	}
	defer func() {
		os.Remove(tmp.Name())
		tmp.Close()
	}()

	// LimitReader protects against decompression bombs
	_, err = io.Copy(tmp, io.LimitReader(r, sizeLimit))
	if err != nil {
		return nil, err
	}

	_, thumb, err = Process(tmp, opts)
	if err != nil {
		err = ErrArchive{err}
	}
	return thumb, err
}

// Thumbnail the first image of a rar file
func processRar(rs io.ReadSeeker, src *Source, opts Options) (thumb image.Image, err error) {
	dec, err := rardecode.NewReader(rs, "")
	if err != nil {
		return nil, err
	}

	var (
		imageCount = 0
		i          = 0
		h          *rardecode.FileHeader
		wg         sync.WaitGroup
	)

	// Only check the first 10 files. We don't need to check them all.
	for i = 0; i < 10; i++ {
		h, err = dec.Next()
		switch err {
		case nil:
		case io.EOF:
			err = nil
		default:
			return nil, err
		}
		if err != nil {
			break
		}
		if h.IsDir {
			continue
		}
		wg.Add(1)

		go func(header *rardecode.FileHeader) {
			defer wg.Done()

			if couldBeImage(header.Name) {
				localThumb, localErr := thumbnailArchiveImage(dec, opts, 100<<20)
				if localErr != nil {
					err = localErr
					return
				} else if localThumb != nil {
					imageCount++
					if imageCount == 1 {
						thumb = localThumb
					}
				}
			}
		}(h)
	}

	wg.Wait()

	if thumb == nil {
		return nil, ErrCantThumbnail
	}

	// If at least 90% of the first 10 files in the archive are images, this is a comic archive
	if float32(imageCount)/float32(i) >= 0.9 {
		src.Mime = "application/vnd.comicbook-rar"
		src.Extension = "cbr"
	}

	return thumb, nil
}
