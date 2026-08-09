package main

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/source"
)

func TestResolveAssocOutput_DICOMDir(t *testing.T) {
	dir := t.TempDir()
	series := filepath.Join(dir, "slide")
	if err := os.MkdirAll(series, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveAssocOutputDICOM(series, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "slide_edited") {
		t.Errorf("got %q", got)
	}
}

func TestRGBAssoc_ImplementsInterface(t *testing.T) {
	img := &decoder.Image{Width: 4, Height: 3, Stride: 12, Format: decoder.PixelFormatRGB, Pix: make([]byte, 36)}
	var a source.AssociatedImage = &rgbAssoc{typ: "label", img: img}
	if a.Type() != "label" {
		t.Errorf("Type = %q", a.Type())
	}
	if got := a.Size(); got != (image.Point{X: 4, Y: 3}) {
		t.Errorf("Size = %v", got)
	}
	if a.Compression() != source.CompressionNone {
		t.Errorf("Compression = %v, want none", a.Compression())
	}
	di, err := a.Decode(decoder.DecodeOptions{})
	if err != nil || di.Width != 4 {
		t.Errorf("Decode = %v, %v", di, err)
	}
	if _, ok := a.Source(); ok {
		t.Errorf("Source ok = true, want false (synthesized)")
	}
}
