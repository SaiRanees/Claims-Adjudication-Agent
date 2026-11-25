package documents

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// Synthetic fixtures for the file checks, built in code so no binary file is
// committed. Every PDF here is written by hand: a header, numbered objects,
// an xref table with real offsets and a trailer.

// pdfObjects assembles objects 1..n (object i is objs[i-1]) into a PDF whose
// catalog is object 1.
func pdfObjects(objs []string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

// textPDF has n A4 pages, each with a line of text in Helvetica.
func textPDF(n int) []byte {
	// 1 catalog, 2 pages, 3 font, then a page and its content per page.
	kids := make([]string, n)
	for i := range n {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for i := range n {
		content := fmt.Sprintf("BT /F1 12 Tf 72 770 Td (SYNTHETIC TEST DOCUMENT page %d of %d) Tj ET", i+1, n)
		objs = append(objs,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	return pdfObjects(objs)
}

// streamPDF is a one-page PDF whose content stream is raw, under filter
// ("" for none).
func streamPDF(raw []byte, filter string) []byte {
	f := ""
	if filter != "" {
		f = " /Filter /" + filter
	}
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d%s >>\nstream\n%s\nendstream", len(raw), f, raw),
	}
	return pdfObjects(objs)
}

// bombPDF's page content inflates to `inflated` bytes of spaces, from a
// Flate stream of a few hundred KiB.
func bombPDF(t testing.TB, inflated int64) []byte {
	t.Helper()
	var z bytes.Buffer
	w, err := zlib.NewWriterLevel(&z, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(w, repeatReader(' '), inflated); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return streamPDF(z.Bytes(), "FlateDecode")
}

type repeatReader byte

func (r repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

// encryptedPDF encrypts textPDF(1) with AES-256, with the given user password
// (which may be empty) and an owner password.
func encryptedPDF(t testing.TB, userPW string) []byte {
	t.Helper()
	conf := model.NewAESConfiguration(userPW, "synthetic-owner", 256)
	var out bytes.Buffer
	if err := api.Encrypt(t.Context(), bytes.NewReader(textPDF(1)), &out, conf); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return out.Bytes()
}

// photo is a w×h synthetic image with a dark bar along its left edge, so its
// orientation shows once rendered. With EXIF orientation 6 (turn 90°
// clockwise to display), the bar belongs at the top.
func photo(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{R: 230, G: 230, B: 230, A: 255}
			if x < w/8 {
				c = color.RGBA{R: 20, G: 20, B: 120, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

// jpegWithOrientation encodes a w×h JPEG and, unless orientation is 0, adds
// an APP1 Exif segment holding it.
func jpegWithOrientation(t testing.TB, w, h int, orientation uint16) []byte {
	t.Helper()
	var enc bytes.Buffer
	if err := jpeg.Encode(&enc, photo(w, h), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	if orientation == 0 {
		return enc.Bytes()
	}
	seg := append([]byte("Exif\x00\x00"), exifTIFF(orientation)...)
	app1 := binary.BigEndian.AppendUint16([]byte{0xFF, 0xE1}, uint16(len(seg)+2)) //nolint:gosec // a few dozen bytes
	app1 = append(app1, seg...)
	out := append([]byte{0xFF, 0xD8}, app1...)
	return append(out, enc.Bytes()[2:]...)
}

// exifTIFF is a big-endian TIFF header with one IFD entry: orientation.
func exifTIFF(orientation uint16) []byte {
	t := []byte("MM\x00\x2a\x00\x00\x00\x08") // byte order, magic, IFD at 8
	t = binary.BigEndian.AppendUint16(t, 1)   // one entry
	t = binary.BigEndian.AppendUint16(t, 0x0112)
	t = binary.BigEndian.AppendUint16(t, 3) // SHORT
	t = binary.BigEndian.AppendUint32(t, 1) // count
	t = binary.BigEndian.AppendUint16(t, orientation)
	t = append(t, 0, 0)                        // value padding
	return binary.BigEndian.AppendUint32(t, 0) // no next IFD
}

func pngImage(t testing.TB, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, photo(w, h)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// hugePNG is a PNG signature and an IHDR claiming w×h, then a tiny IDAT that
// couldn't fill it: only a header read survives it.
func hugePNG(w, h uint32) []byte {
	chunk := func(kind string, data []byte) []byte {
		c := binary.BigEndian.AppendUint32(nil, uint32(len(data))) //nolint:gosec // a fixture chunk is small
		c = append(c, kind...)
		c = append(c, data...)
		return binary.BigEndian.AppendUint32(c, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	}
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 2, 0, 0, 0) // 8-bit RGB
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	_, _ = zw.Write(make([]byte, 1024))
	_ = zw.Close()
	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, chunk("IHDR", ihdr)...)
	out = append(out, chunk("IDAT", z.Bytes())...)
	return append(out, chunk("IEND", nil)...)
}
