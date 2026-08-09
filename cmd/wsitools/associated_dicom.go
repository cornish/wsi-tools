package main

import (
	"errors"
	"image"

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
