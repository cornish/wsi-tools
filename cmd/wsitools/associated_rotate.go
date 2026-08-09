package main

import (
	"fmt"
	"image"
	"strings"

	"github.com/wsilabs/opentile-go/decoder"

	"github.com/wsilabs/wsitools/internal/source"
)

// rotatableTypes gates which associated-image types support rotate. Rotate is
// label-only: the label is the one associated image whose orientation is
// scanner-dependent and routinely needs correcting, and routing it through the
// per-format lossless replace default preserves its encoding.
var rotatableTypes = map[string]bool{"label": true}

// decoderRGBToImage builds an opaque *image.NRGBA from a packed-RGB
// *decoder.Image (the inverse of imageToRGB). Used to feed a rotated label back
// through the image.Image-based TIFF-family / COG-WSI / OME-TIFF replace paths.
func decoderRGBToImage(di *decoder.Image) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, di.Width, di.Height))
	for y := 0; y < di.Height; y++ {
		for x := 0; x < di.Width; x++ {
			si := y*di.Stride + x*3
			o := img.PixOffset(x, y)
			img.Pix[o+0] = di.Pix[si+0]
			img.Pix[o+1] = di.Pix[si+1]
			img.Pix[o+2] = di.Pix[si+2]
			img.Pix[o+3] = 0xFF
		}
	}
	return img
}

// tightRGBPacked returns a tightly-packed (stride == Width*3) copy of di's RGB
// pixels. rotateRGB assumes stride == w*3, but a decoder may hand back a padded
// row stride, so re-pack defensively. When already tight, this is a plain copy.
func tightRGBPacked(di *decoder.Image) []byte {
	out := make([]byte, di.Width*di.Height*3)
	rowBytes := di.Width * 3
	for y := 0; y < di.Height; y++ {
		copy(out[y*rowBytes:(y+1)*rowBytes], di.Pix[y*di.Stride:y*di.Stride+rowBytes])
	}
	return out
}

// runAssociatedRotateFor rotates the label associated image CLOCKWISE by
// degrees ∈ {90,180,270} and writes the result. It decodes the existing label,
// rotates it in memory, and routes the rotated raster through the normal replace
// path (via replaceFlags.preImg) — so the per-format lossless label encoding
// (TIFF-family LZW+Predictor, DICOM native RGB, IFE PNG) is preserved with no
// extra flags. 90/270 swap the label's width and height.
func runAssociatedRotateFor(typ string, degrees int, input, outPath string, fl replaceFlags) error {
	if !rotatableTypes[typ] {
		return fmt.Errorf("%w: rotate is not supported for %s (label only)", ErrUnsupportedAssoc, typ)
	}
	if degrees != 90 && degrees != 180 && degrees != 270 {
		return fmt.Errorf("rotate: degrees must be 90, 180, or 270 (got %d)", degrees)
	}
	src, err := source.Open(input)
	if err != nil {
		return err
	}
	if err := gateFormat(src); err != nil {
		src.Close()
		return err
	}
	var target source.AssociatedImage
	for _, a := range src.Associated() {
		if strings.EqualFold(a.Type(), typ) {
			target = a
			break
		}
	}
	if target == nil {
		src.Close()
		return fmt.Errorf("no %s image to rotate", typ)
	}
	di, err := target.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
	if err != nil {
		src.Close()
		return err
	}
	rgb := tightRGBPacked(di)
	rot, ow, oh := rotateRGB(rgb, di.Width, di.Height, degrees)
	src.Close()

	// The rotated image is used verbatim (its own dims are the target), so no
	// resize/letterbox happens — but the TIFF-family replace path still parses
	// fl.bgHex, which rejects an empty string. Populate the same defaults the
	// `replace` command binds so the shared path is happy.
	if fl.bgHex == "" {
		fl.bgHex = "F5F5E6"
	}
	if fl.resize == "" {
		fl.resize = "fit"
	}
	fl.preImg = &decoder.Image{Width: ow, Height: oh, Stride: ow * 3, Format: decoder.PixelFormatRGB, Pix: rot}
	return runAssociatedReplaceFor(typ, input, outPath, fl)
}

// rotateRGB rotates a tightly-packed (stride=w*3) RGB buffer CLOCKWISE by
// degrees ∈ {90,180,270}. 90/270 swap width and height. Any other degrees value
// (including 0) returns the input unchanged.
func rotateRGB(pix []byte, w, h, degrees int) (out []byte, ow, oh int) {
	get := func(x, y int) (byte, byte, byte) {
		i := (y*w + x) * 3
		return pix[i], pix[i+1], pix[i+2]
	}
	switch degrees {
	case 90:
		ow, oh = h, w
		out = make([]byte, len(pix))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, g, b := get(x, y)
				nx, ny := h-1-y, x
				o := (ny*ow + nx) * 3
				out[o], out[o+1], out[o+2] = r, g, b
			}
		}
	case 270:
		ow, oh = h, w
		out = make([]byte, len(pix))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, g, b := get(x, y)
				nx, ny := y, w-1-x
				o := (ny*ow + nx) * 3
				out[o], out[o+1], out[o+2] = r, g, b
			}
		}
	case 180:
		ow, oh = w, h
		out = make([]byte, len(pix))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, g, b := get(x, y)
				nx, ny := w-1-x, h-1-y
				o := (ny*ow + nx) * 3
				out[o], out[o+1], out[o+2] = r, g, b
			}
		}
	default:
		return pix, w, h
	}
	return out, ow, oh
}
