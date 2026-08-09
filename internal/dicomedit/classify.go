// Package dicomedit provides DICOM-series file-level utilities that sit BELOW
// opentile-go's decoded abstraction: it classifies the .dcm instances in a WSM
// series directory by role and reads series-shared UIDs, so a surgical
// associated-image edit can touch a single instance without re-emitting the
// pyramid.
package dicomedit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/WSILabs/dicom"
	"github.com/WSILabs/dicom/pkg/tag"
)

// Role classifies a DICOM instance by its ImageType (0008,0008) value[2].
type Role string

const (
	RoleLevel     Role = "level"
	RoleLabel     Role = "label"
	RoleOverview  Role = "overview"
	RoleThumbnail Role = "thumbnail"
	RoleMacro     Role = "macro"
	RoleOther     Role = "other"
)

// InstanceInfo holds the path and classified role of a single .dcm instance.
type InstanceInfo struct {
	Path string
	Role Role
}

// ClassifyInstances enumerates *.dcm files in dir and classifies each by its
// ImageType (0008,0008) value[2]. Pixel data is never read.
//
// Classification:
//   - VOLUME   → RoleLevel
//   - LABEL    → RoleLabel
//   - OVERVIEW → RoleOverview
//   - THUMBNAIL → RoleThumbnail
//   - anything else (or missing ImageType) → RoleOther
func ClassifyInstances(dir string) ([]InstanceInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("dicomedit: read dir %s: %w", dir, err)
	}

	var out []InstanceInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.EqualFold(filepath.Ext(e.Name()), ".dcm") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		role, err := classifyOne(path)
		if err != nil {
			return nil, err
		}
		out = append(out, InstanceInfo{Path: path, Role: role})
	}
	return out, nil
}

// classifyOne parses a single .dcm file (stop-before-pixels) and returns its
// role based on ImageType[2].
func classifyOne(path string) (Role, error) {
	ds, err := dicom.ParseFile(path, nil, dicom.SkipPixelData())
	if err != nil {
		return RoleOther, fmt.Errorf("dicomedit: parse %s: %w", path, err)
	}

	el, err := ds.FindElementByTag(tag.ImageType)
	if err != nil {
		// Missing ImageType — not an error, just classify as other.
		return RoleOther, nil
	}

	vals, ok := el.Value.GetValue().([]string)
	if !ok || len(vals) < 3 {
		return RoleOther, nil
	}

	flavor := strings.ToUpper(strings.TrimSpace(vals[2]))
	switch flavor {
	case "VOLUME":
		return RoleLevel, nil
	case "LABEL":
		return RoleLabel, nil
	case "OVERVIEW":
		return RoleOverview, nil
	case "THUMBNAIL":
		return RoleThumbnail, nil
	case "MACRO":
		return RoleMacro, nil
	default:
		return RoleOther, nil
	}
}
