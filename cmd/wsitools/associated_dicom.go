package main

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/source"
)

// rgbAssoc is a synthesized source.AssociatedImage over an in-memory RGB raster —
// a replacement or rotated associated image. Compression()==None routes DICOM's
// writeAssociated down the decode→native-RGB path (lossless). Bytes/Source are
// unavailable (nothing on disk).
type rgbAssoc struct {
	typ string
	img *decoder.Image // PixelFormatRGB
}

func (a *rgbAssoc) Type() string      { return a.typ }
func (a *rgbAssoc) Size() image.Point { return image.Point{X: a.img.Width, Y: a.img.Height} }
func (a *rgbAssoc) Compression() source.Compression {
	return source.CompressionNone
}
func (a *rgbAssoc) Bytes() ([]byte, error) { return nil, errors.New("rgbAssoc: no encoded bytes") }
func (a *rgbAssoc) Decode(decoder.DecodeOptions) (*decoder.Image, error) {
	return a.img, nil
}
func (a *rgbAssoc) Source() (opentile.AssociatedEncoding, bool) {
	return opentile.AssociatedEncoding{}, false
}
func (a *rgbAssoc) IFDOffset() (int64, bool) { return 0, false }

// resolveAssocOutputDICOM resolves the OUTPUT DIRECTORY for a DICOM edit
// (directory-shaped, unlike the file-based resolveAssocOutput). in may be a
// series directory or a .dcm inside one; the derived default is
// "<seriesdir>_edited". --in-place returns the series directory itself.
func resolveAssocOutputDICOM(in, out string, inPlace, overwrite bool) (string, error) {
	seriesDir := in
	if fi, err := os.Stat(in); err == nil && !fi.IsDir() {
		seriesDir = filepath.Dir(in)
	}
	if inPlace {
		if out != "" {
			return "", fmt.Errorf("--in-place and -o/--output are mutually exclusive")
		}
		return seriesDir, nil
	}
	if out == "" {
		base := strings.TrimRight(seriesDir, string(filepath.Separator))
		out = base + "_edited"
	}
	if _, err := os.Stat(out); err == nil && !overwrite {
		return "", fmt.Errorf("output %s already exists (use --force)", out)
	}
	abs, _ := filepath.Abs(out)
	absSeries, _ := filepath.Abs(seriesDir)
	if abs == absSeries {
		return "", fmt.Errorf("input and output are the same directory: %s", abs)
	}
	return out, nil
}

// isDICOMInput reports whether path opens as a DICOM source (directory series or
// a .dcm). Cheap: extension + directory-contains-.dcm check (avoids a full open).
func isDICOMInput(path string) bool {
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return strings.EqualFold(filepath.Ext(path), ".dcm")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".dcm") {
			return true
		}
	}
	return false
}

// imageToRGB converts a decoded Go image.Image to a tightly-packed RGB
// decoder.Image (the form rgbAssoc + the codecs expect).
func imageToRGB(img image.Image) *decoder.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	pix := make([]byte, w*h*3)
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			pix[i], pix[i+1], pix[i+2] = byte(r>>8), byte(g>>8), byte(bl>>8)
			i += 3
		}
	}
	return &decoder.Image{Width: w, Height: h, Stride: w * 3, Format: decoder.PixelFormatRGB, Pix: pix}
}
