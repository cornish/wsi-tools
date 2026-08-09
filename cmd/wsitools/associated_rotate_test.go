package main

import "testing"

func TestRotateRGB_90SwapsDimsAndTransposes(t *testing.T) {
	// 2x1 image: pixel(0,0)=red, pixel(1,0)=green.
	pix := []byte{255, 0, 0, 0, 255, 0}
	out, ow, oh := rotateRGB(pix, 2, 1, 90)
	if ow != 1 || oh != 2 {
		t.Fatalf("dims = %dx%d, want 1x2", ow, oh)
	}
	// 90° clockwise: the top row (left→right) becomes the right column (top→bottom),
	// so for a 2x1 the output is a 1x2 column: row0=red (old x=0), row1=green (old x=1).
	if !(out[0] == 255 && out[1] == 0 && out[2] == 0) {
		t.Errorf("new(0,0) = %v, want red", out[0:3])
	}
	if !(out[3] == 0 && out[4] == 255 && out[5] == 0) {
		t.Errorf("new(0,1) = %v, want green", out[3:6])
	}
}

func TestRotateRGB_180Dims(t *testing.T) {
	pix := make([]byte, 4*3*3)
	out, ow, oh := rotateRGB(pix, 4, 3, 180)
	if ow != 4 || oh != 3 || len(out) != len(pix) {
		t.Errorf("180 dims/len wrong: %dx%d len=%d", ow, oh, len(out))
	}
}

func TestRotateRGB_270Dims(t *testing.T) {
	pix := make([]byte, 5*2*3)
	out, ow, oh := rotateRGB(pix, 5, 2, 270)
	if ow != 2 || oh != 5 || len(out) != len(pix) {
		t.Errorf("270 dims/len wrong: %dx%d len=%d", ow, oh, len(out))
	}
}

func TestRotatableTypesGate(t *testing.T) {
	if !rotatableTypes["label"] {
		t.Error("label must be rotatable")
	}
	if rotatableTypes["macro"] {
		t.Error("macro must NOT be rotatable")
	}
}
