package dicomedit

import (
	"os"
	"path/filepath"
	"testing"
)

func testdir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("WSI_TOOLS_TESTDIR")
	if d == "" {
		d = "../../sample_files"
	}
	return d
}

func TestClassifyInstances_Grundium(t *testing.T) {
	dir := filepath.Join(testdir(t), "dicom", "scan_621_grundium_dicom")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture: %v", err)
	}
	got, err := ClassifyInstances(dir)
	if err != nil {
		t.Fatalf("ClassifyInstances: %v", err)
	}
	var levels, labels int
	for _, in := range got {
		switch in.Role {
		case RoleLevel:
			levels++
		case RoleLabel:
			labels++
		}
		if in.Path == "" {
			t.Errorf("empty path in %+v", in)
		}
	}
	if levels < 1 {
		t.Errorf("want >=1 level instance, got %d (of %d)", levels, len(got))
	}
}
