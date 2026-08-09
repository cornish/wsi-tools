//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// labelDims returns the width/height of the label associated image as reported
// by `info --json <path>`, or (0,0) if there is no label.
func labelDims(t *testing.T, bin, path string) (int, int) {
	t.Helper()
	out, err := exec.Command(bin, "info", "--json", path).Output()
	if err != nil {
		t.Fatalf("info --json %s: %v", path, err)
	}
	var res struct {
		Associated []struct {
			Type   string `json:"type"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"associated_images"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal info json: %v\n%s", err, out)
	}
	for _, a := range res.Associated {
		if a.Type == "label" {
			return a.Width, a.Height
		}
	}
	return 0, 0
}

// TestAssocRotate_DICOMLabel90 rotates a DICOM label 90° and confirms the label
// survives with its width/height swapped (native/lossless label encoding).
func TestAssocRotate_DICOMLabel90(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "dicom", "Leica-4")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("no Leica-4 DICOM fixture")
	}
	if !containsLabelAssoc(t, bin, src) {
		t.Skip("fixture has no label to rotate")
	}

	ow, oh := labelDims(t, bin, src)
	if ow == 0 || oh == 0 {
		t.Fatalf("could not read source label dims")
	}

	out := filepath.Join(t.TempDir(), "rotated")
	if o, err := exec.Command(bin, "label", "rotate", "90", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("label rotate 90 <dicom>: %v\n%s", err, o)
	}

	if !containsLabelAssoc(t, bin, out) {
		t.Fatalf("label missing after rotate")
	}
	nw, nh := labelDims(t, bin, out)
	if nw != oh || nh != ow {
		t.Errorf("rotated label dims = %dx%d, want %dx%d (swap of %dx%d)", nw, nh, oh, ow, ow, oh)
	}
	t.Logf("DICOM label: %dx%d -> %dx%d", ow, oh, nw, nh)
}

// TestAssocRotate_SVSLabel90 rotates an SVS label 90° and confirms the output
// still opens, the label is present, and its dims swap (LZW/lossless preserved).
func TestAssocRotate_SVSLabel90(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("no CMU-1-Small-Region.svs fixture")
	}
	if !containsLabelAssoc(t, bin, src) {
		t.Skip("fixture has no label to rotate")
	}

	ow, oh := labelDims(t, bin, src)

	out := filepath.Join(t.TempDir(), "rotated.svs")
	if o, err := exec.Command(bin, "label", "rotate", "90", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("label rotate 90 <svs>: %v\n%s", err, o)
	}

	// Output must open.
	if o, err := exec.Command(bin, "info", out).CombinedOutput(); err != nil {
		t.Fatalf("info on rotated svs failed: %v\n%s", err, o)
	}
	if !containsLabelAssoc(t, bin, out) {
		t.Fatalf("label missing after rotate")
	}
	nw, nh := labelDims(t, bin, out)
	if ow != oh {
		// Non-square label: dims should swap.
		if nw != oh || nh != ow {
			t.Errorf("rotated label dims = %dx%d, want %dx%d (swap of %dx%d)", nw, nh, oh, ow, ow, oh)
		}
	}
	t.Logf("SVS label: %dx%d -> %dx%d", ow, oh, nw, nh)
}

// TestAssocRotate_BadDegrees confirms an unsupported rotation angle is rejected
// with a clear error, and that `macro rotate` does not perform a rotation
// (macro is not a rotatable type — no macro rotate subcommand is registered).
func TestAssocRotate_BadDegrees(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("no CMU-1-Small-Region.svs fixture")
	}

	// Bad degrees on a valid (label) rotate must fail non-zero with a clear msg.
	o, err := exec.Command(bin, "label", "rotate", "45", src).CombinedOutput()
	if err == nil {
		t.Errorf("label rotate 45 should fail, but succeeded\n%s", o)
	}
	if !strings.Contains(string(o), "90, 180, or 270") {
		t.Errorf("bad-degrees error should mention valid angles, got:\n%s", o)
	}

	// `macro rotate` must NOT perform a rotation: rotate is registered only for
	// label, so no output file is written and no "replaced/rotated" success line
	// is emitted. (cobra treats it as an unknown subcommand of the macro group.)
	tmpOut := filepath.Join(t.TempDir(), "macro-rot.svs")
	o2, _ := exec.Command(bin, "macro", "rotate", "90", "-o", tmpOut, src).CombinedOutput()
	if _, statErr := os.Stat(tmpOut); statErr == nil {
		t.Errorf("macro rotate wrote an output file %s — rotate should not be available for macro", tmpOut)
	}
	if bytes.Contains(o2, []byte("replaced")) || bytes.Contains(o2, []byte("rotated")) {
		t.Errorf("macro rotate reported a rotation; it should not be a valid command:\n%s", o2)
	}
}
