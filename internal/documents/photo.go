package documents

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // registers the JPEG header reader for image.DecodeConfig
	_ "image/png"  // registers the PNG header reader for image.DecodeConfig
	"io"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// MaxImagePixels is the most pixels a photo may declare. Anything larger is
// rejected from its header, so a decompression bomb is never decoded.
const MaxImagePixels = 50_000_000

// PhotoDeadline bounds pdfcpu's turning one photo into a PDF; pdfium_test.go
// measures it.
const PhotoDeadline = 30 * time.Second

var (
	// ErrImageTooLarge: the header declares more than MaxImagePixels.
	ErrImageTooLarge = errors.New("image is too large")
	// ErrBadImage: the header or EXIF can't be read, or the orientation is
	// mirrored, which cameras don't write.
	ErrBadImage = errors.New("image is unreadable")
)

// pdfcpuConfig never touches pdfcpu's configuration directory, which the
// default would create under the user's config dir.
func pdfcpuConfig() *model.Configuration { return model.NewStatelessConfiguration() }

// ImageHeader reads an image's dimensions and its EXIF orientation as a
// page rotation in degrees (0, 90, 180 or 270), without decoding pixels.
func ImageHeader(data []byte) (width, height, rotate int, err error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("%w: header: %w", ErrBadImage, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, 0, fmt.Errorf("%w: %dx%d", ErrBadImage, cfg.Width, cfg.Height)
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxImagePixels {
		return cfg.Width, cfg.Height, 0, fmt.Errorf("%w: %dx%d", ErrImageTooLarge, cfg.Width, cfg.Height)
	}
	orientation, err := exifOrientation(data)
	if err != nil {
		return 0, 0, 0, err
	}
	switch orientation {
	case 1:
		rotate = 0
	case 3:
		rotate = 180
	case 6:
		rotate = 90
	case 8:
		rotate = 270
	default: // 2, 4, 5 and 7 are mirrored; anything else is invalid
		return 0, 0, 0, fmt.Errorf("%w: EXIF orientation %d", ErrBadImage, orientation)
	}
	return cfg.Width, cfg.Height, rotate, nil
}

// PhotoToPDF turns a photo whose header ImageHeader accepted into a one-page
// PDF, with rotate set as the page's /Rotate so it displays upright. pdfcpu
// runs under ctx's deadline, and its panics come back as errors (pdfcpu
// recovers them itself; this recovers anything it misses).
func PhotoToPDF(ctx context.Context, data []byte, rotate int, deadline time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	type result struct {
		pdf []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		defer func() {
			if p := recover(); p != nil {
				r = result{err: fmt.Errorf("%w: pdfcpu panic: %v", ErrBadImage, p)}
			}
			done <- r
		}()
		r.pdf, r.err = importPhoto(ctx, data, rotate)
	}()
	select {
	case r := <-done:
		return r.pdf, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: pdfcpu: %w", ErrBadImage, ctx.Err())
	}
}

func importPhoto(ctx context.Context, data []byte, rotate int) ([]byte, error) {
	conf := pdfcpuConfig()
	var page bytes.Buffer
	if err := api.ImportImages(ctx, nil, &page, []io.Reader{bytes.NewReader(data)}, api.DefaultImportConfig(), conf); err != nil {
		return nil, fmt.Errorf("%w: import: %w", ErrBadImage, err)
	}
	if rotate == 0 {
		return page.Bytes(), nil
	}
	var rotated bytes.Buffer
	if err := api.Rotate(ctx, bytes.NewReader(page.Bytes()), &rotated, rotate, nil, conf); err != nil {
		return nil, fmt.Errorf("%w: rotate: %w", ErrBadImage, err)
	}
	return rotated.Bytes(), nil
}

// exifOrientation finds tag 0x0112 in a JPEG's APP1 Exif segment or a PNG's
// eXIf chunk. No EXIF, or no orientation tag, is 1 (upright).
func exifOrientation(data []byte) (int, error) {
	var tiff []byte
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8}):
		tiff = jpegExif(data)
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		tiff = pngExif(data)
	}
	if tiff == nil {
		return 1, nil
	}
	return tiffOrientation(tiff)
}

func jpegExif(data []byte) []byte {
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return nil
		}
		marker := data[i+1]
		if marker == 0xD8 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 || marker == 0xFF {
			i += 2
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // start of scan, end of image
			return nil
		}
		n := int(binary.BigEndian.Uint16(data[i+2:]))
		if n < 2 || i+2+n > len(data) {
			return nil
		}
		seg := data[i+4 : i+2+n]
		if marker == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return seg[6:]
		}
		i += 2 + n
	}
	return nil
}

func pngExif(data []byte) []byte {
	for i := 8; i+8 <= len(data); {
		n := int(binary.BigEndian.Uint32(data[i:]))
		kind := string(data[i+4 : i+8])
		if n < 0 || i+12+n > len(data) || kind == "IDAT" || kind == "IEND" {
			return nil
		}
		if kind == "eXIf" {
			return data[i+8 : i+8+n]
		}
		i += 12 + n
	}
	return nil
}

func tiffOrientation(t []byte) (int, error) {
	if len(t) < 8 {
		return 0, fmt.Errorf("%w: short EXIF", ErrBadImage)
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0, fmt.Errorf("%w: EXIF byte order", ErrBadImage)
	}
	if order.Uint16(t[2:]) != 42 {
		return 0, fmt.Errorf("%w: EXIF magic", ErrBadImage)
	}
	ifd := int(order.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 0, fmt.Errorf("%w: EXIF IFD offset", ErrBadImage)
	}
	entries := int(order.Uint16(t[ifd:]))
	for e := range entries {
		at := ifd + 2 + 12*e
		if at+12 > len(t) {
			return 0, fmt.Errorf("%w: EXIF IFD runs past its segment", ErrBadImage)
		}
		if order.Uint16(t[at:]) != 0x0112 {
			continue
		}
		if order.Uint16(t[at+2:]) != 3 || order.Uint32(t[at+4:]) != 1 { // one SHORT
			return 0, fmt.Errorf("%w: EXIF orientation type", ErrBadImage)
		}
		return int(order.Uint16(t[at+8:])), nil
	}
	return 1, nil
}
