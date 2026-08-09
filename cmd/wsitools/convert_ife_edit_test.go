package main

import (
	"testing"

	"github.com/wsilabs/opentile-go/decoder"
)

func TestEncodeAssocPNG(t *testing.T) {
	di := &decoder.Image{Width: 8, Height: 6, Stride: 24, Format: decoder.PixelFormatRGB, Pix: make([]byte, 8*6*3)}
	blob, w, h, err := encodeAssocPNG(di)
	if err != nil {
		t.Fatal(err)
	}
	if w != 8 || h != 6 {
		t.Errorf("dims = %dx%d", w, h)
	}
	if len(blob) < 8 || string(blob[1:4]) != "PNG" {
		t.Errorf("not a PNG: % x", blob[:min(8, len(blob))])
	}
}
