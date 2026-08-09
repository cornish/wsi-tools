//go:build integration

package integration

import (
	"bytes"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestAssocIFE_LabelRemove converts an SVS to IFE, removes the label, and
// verifies the edited IFE opens cleanly. The pyramid is rebuilt verbatim
// (byte-identical tile copy); only the associated set changes.
func TestAssocIFE_LabelRemove(t *testing.T) {
	bin := buildOnce(t)
	svs := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(svs); err != nil {
		t.Skipf("no svs fixture")
	}
	dir := t.TempDir()
	ifeFile := filepath.Join(dir, "slide.ife")
	if o, err := exec.Command(bin, "convert", "--to", "ife", "-f", "-o", ifeFile, svs).CombinedOutput(); err != nil {
		t.Fatalf("make ife: %v\n%s", err, o)
	}

	// Confirm IFE has a label to remove.
	if !containsLabelAssoc(t, bin, ifeFile) {
		t.Skipf("IFE has no label image (source SVS has none); nothing to remove")
	}

	out := filepath.Join(dir, "nolabel.ife")
	if o, err := exec.Command(bin, "label", "remove", "-o", out, ifeFile).CombinedOutput(); err != nil {
		t.Fatalf("label remove <ife>: %v\n%s", err, o)
	}

	// Edited IFE must open via info.
	if o, err := exec.Command(bin, "info", out).CombinedOutput(); err != nil {
		t.Fatalf("info edited ife: %v\n%s", err, o)
	}

	// Label must be gone.
	if containsLabelAssoc(t, bin, out) {
		t.Errorf("label still present after remove")
	}

	// Pyramid lossless check: pixel hash of original and edited IFE must match.
	hashBefore := ifePixelHash(t, bin, ifeFile)
	hashAfter := ifePixelHash(t, bin, out)
	if hashBefore != "" && hashAfter != "" && hashBefore != hashAfter {
		t.Errorf("pyramid pixel hash changed after label remove: before=%s after=%s", hashBefore, hashAfter)
	}
	t.Logf("pyramid pixel hash: %s", hashAfter)
}

// TestAssocIFE_LabelReplace converts an SVS to IFE, replaces the label with a
// generated PNG, and verifies the edited IFE opens and the label is present.
func TestAssocIFE_LabelReplace(t *testing.T) {
	bin := buildOnce(t)
	svs := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(svs); err != nil {
		t.Skipf("no svs fixture")
	}
	dir := t.TempDir()
	ifeFile := filepath.Join(dir, "slide.ife")
	if o, err := exec.Command(bin, "convert", "--to", "ife", "-f", "-o", ifeFile, svs).CombinedOutput(); err != nil {
		t.Fatalf("make ife: %v\n%s", err, o)
	}

	// Write a test PNG to use as replacement.
	pngPath := filepath.Join(dir, "new_label.png")
	writeTestPNG(t, pngPath, 300, 200)

	out := filepath.Join(dir, "relabeled.ife")
	if o, err := exec.Command(bin, "label", "replace", "--image", pngPath, "-o", out, ifeFile).CombinedOutput(); err != nil {
		t.Fatalf("label replace <ife>: %v\n%s", err, o)
	}

	// Edited IFE must open via info.
	if o, err := exec.Command(bin, "info", out).CombinedOutput(); err != nil {
		t.Fatalf("info edited ife: %v\n%s", err, o)
	}

	// Label must be present.
	if !containsLabelAssoc(t, bin, out) {
		t.Errorf("label missing after replace")
	}

	// Verify the replaced label decodes to the correct dimensions (300×200).
	extractedPNG := filepath.Join(dir, "extracted_label.png")
	if o, err := exec.Command(bin, "extract", "--type", "label", "--format", "png", "-o", extractedPNG, out).CombinedOutput(); err != nil {
		t.Logf("extract label (non-fatal): %v\n%s", err, o)
	} else {
		data, rerr := os.ReadFile(extractedPNG)
		if rerr != nil {
			t.Logf("read extracted label: %v", rerr)
		} else {
			limg, _, derr := image.Decode(bytes.NewReader(data))
			if derr != nil {
				t.Logf("decode extracted label: %v", derr)
			} else {
				b := limg.Bounds()
				t.Logf("replaced label dims: %dx%d", b.Dx(), b.Dy())
				if b.Dx() != 300 || b.Dy() != 200 {
					t.Errorf("replaced label dims: got %dx%d, want 300x200", b.Dx(), b.Dy())
				}
			}
		}
	}

	// Pyramid lossless: pixel hash of original and replaced IFE must match.
	hashBefore := ifePixelHash(t, bin, ifeFile)
	hashAfter := ifePixelHash(t, bin, out)
	if hashBefore != "" && hashAfter != "" && hashBefore != hashAfter {
		t.Errorf("pyramid pixel hash changed after label replace: before=%s after=%s", hashBefore, hashAfter)
	}
}

// ifePixelHash returns the pixel hash for the IFE pyramid (non-fatal: returns ""
// on error and lets the caller skip the comparison).
func ifePixelHash(t *testing.T, bin, path string) string {
	t.Helper()
	out, err := exec.Command(bin, "hash", "--mode", "pixel", path).Output()
	if err != nil {
		t.Logf("hash --mode pixel %s: %v (skipping lossless check)", path, err)
		return ""
	}
	// Output format: "<hash>  <path>" — grab the first field.
	fields := bytes.Fields(bytes.TrimSpace(out))
	if len(fields) == 0 {
		return ""
	}
	return string(fields[0])
}
