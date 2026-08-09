//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// containsLabelAssoc reports whether `info --json <path>` lists a label associated image.
func containsLabelAssoc(t *testing.T, bin, path string) bool {
	t.Helper()
	out, err := exec.Command(bin, "info", "--json", path).Output()
	if err != nil {
		t.Fatalf("info --json %s: %v", path, err)
	}
	var res struct {
		Associated []struct {
			Type string `json:"type"`
		} `json:"associated_images"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal info json: %v\n%s", err, out)
	}
	for _, a := range res.Associated {
		if a.Type == "label" {
			return true
		}
	}
	return false
}

// levelDCMNames returns the basenames of .dcm files classified as VOLUME/level.
// We proxy this by asking `info --json` for the level count and then comparing
// file counts, but a simpler approach is: any .dcm that is NOT an associated
// (label/overview/thumbnail/macro) by our classification is a level. Since we
// can't call dicomedit directly, we rely on file-count and byte-identity:
// after remove, the output dir should have exactly one fewer .dcm than the
// source, and every remaining .dcm should be byte-identical to the same-named
// file in the source.
func dcmFileMap(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	m := make(map[string][]byte)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".dcm" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		m[e.Name()] = data
	}
	return m
}

// label remove on a DICOM series drops the label instance and keeps the levels.
func TestAssocDICOM_LabelRemove(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "dicom", "Leica-4")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("no Leica-4 DICOM fixture")
	}
	if !containsLabelAssoc(t, bin, src) {
		t.Skip("fixture has no label to remove")
	}

	srcFiles := dcmFileMap(t, src)

	out := filepath.Join(t.TempDir(), "edited")
	if o, err := exec.Command(bin, "label", "remove", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("label remove <dicom>: %v\n%s", err, o)
	}

	// info should still open the output series.
	if o, err := exec.Command(bin, "info", out).CombinedOutput(); err != nil {
		t.Fatalf("info on edited series failed: %v\n%s", err, o)
	}

	// Label must be gone.
	if containsLabelAssoc(t, bin, out) {
		t.Errorf("label still present after remove")
	}

	// Output must have exactly one fewer .dcm than source.
	outFiles := dcmFileMap(t, out)
	if len(outFiles) != len(srcFiles)-1 {
		t.Errorf("output has %d .dcm files, want %d (src %d - 1)", len(outFiles), len(srcFiles)-1, len(srcFiles))
	}

	// Every file that appears in BOTH source and output must be byte-identical
	// (surgical guarantee: level instances are copied verbatim).
	identical := 0
	for name, outData := range outFiles {
		srcData, ok := srcFiles[name]
		if !ok {
			// File exists only in output — unexpected (we copy src→out, not add).
			t.Errorf("output contains unexpected file not in source: %s", name)
			continue
		}
		if !bytes.Equal(srcData, outData) {
			t.Errorf("file %s differs between source and output (not byte-identical)", name)
		} else {
			identical++
		}
	}
	if identical == 0 {
		t.Errorf("no files were byte-identical — same-name matching may have failed; outFiles=%v", outFiles)
	}
	t.Logf("byte-identical level files: %d/%d", identical, len(outFiles))
}
