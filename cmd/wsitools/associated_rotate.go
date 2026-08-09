package main

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
