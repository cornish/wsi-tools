# Associated-image editing for DICOM & IFE, + `label rotate` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `remove`/`replace` associated-image editing to DICOM-WSI and IFE, and add a `label rotate {90,180,270}` verb across all editable formats.

**Architecture:** DICOM = surgical single-instance edit (touch only the label `.dcm`, pyramid instances copied verbatim); IFE = writer rebuild (pyramid verbatim, associated re-encoded to PNG); `rotate` = type-generic op on the replace path, gated to `{label}`, preserving lossless encoding.

**Tech Stack:** Go; `internal/dicomwriter` (on `WSILabs/dicom`), `internal/dicomedit` (new), `internal/ife`, `cmd/wsitools/associated*.go`, `internal/source`.

**Spec:** `docs/superpowers/specs/2026-07-21-associated-editing-dicom-ife-rotate-design.md`

**Conventions:** branch off `main` (`feat/assoc-editing-dicom-ife-rotate`); commit trailer `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`; scratch in `/Volumes/Ext/tmp`; integration tests need `WSI_TOOLS_TESTDIR=$(pwd)/sample_files` and `-tags integration`; prefix heavy runs with `TMPDIR=/Volumes/Ext/tmp`.

---

## File structure

**New:**
- `internal/dicomedit/classify.go` — enumerate + classify a DICOM series directory's `.dcm` files by role (level / label / macro / thumbnail / overview / other) and read series-shared UIDs. Below opentile's decoded abstraction; reads tags via `WSILabs/dicom`.
- `internal/dicomedit/classify_test.go` — unit tests.
- `cmd/wsitools/associated_dicom.go` — `runAssociated{Remove,Replace}ForDICOM`, the directory-output helper, and the synthetic `source.AssociatedImage` for a replacement RGB.
- `cmd/wsitools/associated_ife.go` — `runAssociated{Remove,Replace}ForIFE`.
- `cmd/wsitools/associated_rotate.go` — `runAssociatedRotateFor`, `rotatableTypes`, `rotateRGB`.

**Modified:**
- `internal/dicomwriter/dicomwriter.go` — export `SharedUIDs` + `WriteAssociatedInstance`.
- `cmd/wsitools/associated.go` — `assocFormatSupported` (+DICOM,+IFE), `gateFormat` message, dispatch branches in `runAssociatedRemoveFor`/`runAssociatedReplaceFor`, DICOM-aware `resolveAssocOutput`, `rotate` subcommand in `newAssocTypeCmd`.
- `cmd/wsitools/convert_ife.go` — thread an edit plan through `assembleIFEMetadata`/`addIFEAssociated`; add `encodeAssocPNG`.
- `docs/commands.md`, `docs/formats.md`, `README.md`, `docs/roadmap.md`.

**Tests:** `tests/integration/assoc_dicom_test.go`, `tests/integration/assoc_ife_test.go`, `tests/integration/assoc_rotate_test.go` (all `//go:build integration`), plus the unit tests noted per task.

---

## Task 0: Branch

- [ ] **Step 1: Create the branch**

```bash
cd /Volumes/Ext/GitHub/wsitools
git checkout main && git pull
git checkout -b feat/assoc-editing-dicom-ife-rotate
```

---

# Phase 1 — DICOM `remove` / `replace`

## Task 1: `internal/dicomedit.ClassifyInstances`

**Files:**
- Create: `internal/dicomedit/classify.go`
- Test: `internal/dicomedit/classify_test.go`

Classify each `.dcm` in a directory by role, reading `ImageType` (0008,0008) with stop-before-pixels. `ImageType[2]` is `VOLUME` for pyramid levels and `LABEL`/`OVERVIEW`/`THUMBNAIL` for associated (per the DICOM WSM IOD and wsitools' own writer, `internal/dicomwriter/dataset.go`). Role is the lowercased flavor; `VOLUME` → `level`.

- [ ] **Step 1: Write the failing test**

Uses the repo's own `convert --to dicom` output as a fixture-free source is not possible in a unit test, so build a tiny series with the existing writer test helpers is overkill — instead classify the integration fixture directly, gated on its presence.

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/dicomedit/ -run TestClassifyInstances -v`
Expected: FAIL — `undefined: ClassifyInstances` / `RoleLevel`.

- [ ] **Step 3: Implement**

```go
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

	"github.com/wsilabs/dicom"
	"github.com/wsilabs/dicom/pkg/tag"
)

type Role string

const (
	RoleLevel     Role = "level"
	RoleLabel     Role = "label"
	RoleOverview  Role = "overview"
	RoleThumbnail Role = "thumbnail"
	RoleMacro     Role = "macro"
	RoleOther     Role = "other"
)

// InstanceInfo is one classified .dcm file in a series directory.
type InstanceInfo struct {
	Path string
	Role Role
}

// ClassifyInstances enumerates *.dcm in dir and classifies each by its
// ImageType (0008,0008) value 3 flavor: VOLUME -> level; LABEL/OVERVIEW/
// THUMBNAIL -> the matching associated role; anything else -> other. Pixel data
// is not read.
func ClassifyInstances(dir string) ([]InstanceInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("dicomedit: read dir %s: %w", dir, err)
	}
	var out []InstanceInfo
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".dcm") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		role, err := classifyOne(p)
		if err != nil {
			return nil, err
		}
		out = append(out, InstanceInfo{Path: p, Role: role})
	}
	return out, nil
}

func classifyOne(path string) (Role, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("dicomedit: open %s: %w", path, err)
	}
	defer f.Close()
	st, _ := f.Stat()
	ds, err := dicom.Parse(f, st.Size(), nil, dicom.SkipPixelData())
	if err != nil {
		return "", fmt.Errorf("dicomedit: parse %s: %w", path, err)
	}
	el, err := ds.FindElementByTag(tag.ImageType)
	if err != nil {
		return RoleOther, nil // no ImageType — treat as non-editable
	}
	vals, _ := el.Value.GetValue().([]string)
	flavor := ""
	if len(vals) >= 3 {
		flavor = strings.ToUpper(strings.TrimSpace(vals[2]))
	}
	switch flavor {
	case "VOLUME":
		return RoleLevel, nil
	case "LABEL":
		return RoleLabel, nil
	case "OVERVIEW":
		return RoleOverview, nil
	case "THUMBNAIL":
		return RoleThumbnail, nil
	default:
		return RoleOther, nil
	}
}
```

> Note: verify `dicom.SkipPixelData()` is the option name in the `WSILabs/dicom` fork (grep `func SkipPixelData` / `func Parse` in the module). If the fork exposes stop-before-pixels differently (e.g. `dicom.ParseUntilEOF` + an option, or a frame-skipping reader), adapt this one call — the classification logic is unchanged.

- [ ] **Step 4: Run test to verify it passes**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./internal/dicomedit/ -run TestClassifyInstances -v`
Expected: PASS (or SKIP if the fixture is absent — then run once with the fixture present before moving on).

- [ ] **Step 5: Commit**

```bash
git add internal/dicomedit/classify.go internal/dicomedit/classify_test.go
git commit -m "feat(dicomedit): classify DICOM series instances by role"
```

---

## Task 2: `internal/dicomedit.ReadSharedUIDs`

**Files:**
- Modify: `internal/dicomedit/classify.go`
- Test: `internal/dicomedit/classify_test.go`

Read the four series-shared UIDs from one existing instance so a replacement instance joins the series.

- [ ] **Step 1: Write the failing test**

```go
func TestReadSharedUIDs_Grundium(t *testing.T) {
	dir := filepath.Join(testdir(t), "dicom", "scan_621_grundium_dicom")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture: %v", err)
	}
	insts, err := ClassifyInstances(dir)
	if err != nil {
		t.Fatal(err)
	}
	var levelPath string
	for _, in := range insts {
		if in.Role == RoleLevel {
			levelPath = in.Path
			break
		}
	}
	if levelPath == "" {
		t.Fatal("no level instance to read UIDs from")
	}
	u, err := ReadSharedUIDs(levelPath)
	if err != nil {
		t.Fatalf("ReadSharedUIDs: %v", err)
	}
	if u.Study == "" || u.Series == "" || u.FrameOfReference == "" {
		t.Errorf("missing shared UID(s): %+v", u)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./internal/dicomedit/ -run TestReadSharedUIDs -v`
Expected: FAIL — `undefined: ReadSharedUIDs`.

- [ ] **Step 3: Implement (append to classify.go)**

`SharedUIDs` will be defined by Task 3 in `internal/dicomwriter`. To avoid an import cycle (dicomwriter must not import dicomedit; dicomedit importing dicomwriter is fine — one direction), return the `dicomwriter.SharedUIDs` type.

```go
import (
	// ...existing...
	dicomwriter "github.com/wsilabs/wsitools/internal/dicomwriter"
)

// ReadSharedUIDs reads the series-level UIDs from one existing instance so a new
// associated instance can join the same series.
func ReadSharedUIDs(path string) (dicomwriter.SharedUIDs, error) {
	f, err := os.Open(path)
	if err != nil {
		return dicomwriter.SharedUIDs{}, fmt.Errorf("dicomedit: open %s: %w", path, err)
	}
	defer f.Close()
	st, _ := f.Stat()
	ds, err := dicom.Parse(f, st.Size(), nil, dicom.SkipPixelData())
	if err != nil {
		return dicomwriter.SharedUIDs{}, fmt.Errorf("dicomedit: parse %s: %w", path, err)
	}
	str := func(t tag.Tag) string {
		el, err := ds.FindElementByTag(t)
		if err != nil {
			return ""
		}
		v, _ := el.Value.GetValue().([]string)
		if len(v) == 0 {
			return ""
		}
		return v[0]
	}
	u := dicomwriter.SharedUIDs{
		Study:            str(tag.StudyInstanceUID),
		Series:           str(tag.SeriesInstanceUID),
		FrameOfReference: str(tag.FrameOfReferenceUID),
		DimensionOrg:     str(tag.DimensionOrganizationUID),
	}
	if u.Study == "" || u.Series == "" || u.FrameOfReference == "" {
		return u, fmt.Errorf("dicomedit: %s missing required shared UID(s)", path)
	}
	return u, nil
}
```

> Do Task 3 (which defines `dicomwriter.SharedUIDs`) BEFORE running this test — or write both, then run. Note the ordering in the commit.

- [ ] **Step 4: Run to verify it passes** (after Task 3 lands `SharedUIDs`)

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./internal/dicomedit/ -run TestReadSharedUIDs -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/dicomedit/classify.go internal/dicomedit/classify_test.go
git commit -m "feat(dicomedit): read series-shared UIDs from an instance"
```

---

## Task 3: Export `SharedUIDs` + `WriteAssociatedInstance` from `internal/dicomwriter`

**Files:**
- Modify: `internal/dicomwriter/dicomwriter.go`
- Test: `internal/dicomwriter/associated_test.go`

Promote the unexported `sharedUIDs` to exported `SharedUIDs` and add an exported single-instance writer wrapping `writeAssociated`.

- [ ] **Step 1: Write the failing test (append to associated_test.go)**

Model on the existing `TestWriteAssociated` (`associated_test.go:41`) which already builds a fake source with associated images. Reuse that file's existing fake-source helper (grep it for the constructor name, e.g. `newFakeSource`/`fakeSource{}`).

```go
func TestWriteAssociatedInstance_Native(t *testing.T) {
	src := newFakeSourceWithAssociated(t) // reuse the helper TestWriteAssociated uses
	shared := SharedUIDs{
		Study:            "1.2.3.1",
		Series:           "1.2.3.2",
		FrameOfReference: "1.2.3.3",
		DimensionOrg:     "1.2.3.4",
	}
	a := src.Associated()[0] // a label
	var buf bytes.Buffer
	if err := WriteAssociatedInstance(&buf, src, a, shared, 99); err != nil {
		t.Fatalf("WriteAssociatedInstance: %v", err)
	}
	ds, err := dicom.Parse(bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	// Belongs to the supplied series.
	el, _ := ds.FindElementByTag(tag.SeriesInstanceUID)
	if got := el.Value.GetValue().([]string)[0]; got != shared.Series {
		t.Errorf("SeriesInstanceUID = %q, want %q", got, shared.Series)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/dicomwriter/ -run TestWriteAssociatedInstance -v`
Expected: FAIL — `undefined: SharedUIDs` / `WriteAssociatedInstance`.

- [ ] **Step 3: Implement**

Rename the type `sharedUIDs` → `SharedUIDs` throughout `internal/dicomwriter/` (it is referenced in `dicomwriter.go`, `dataset.go`; do a package-local rename — `grep -rl sharedUIDs internal/dicomwriter`). Keep `newSharedUIDs()` returning `SharedUIDs`. Then add:

```go
// WriteAssociatedInstance emits ONE associated-image DICOM instance (label /
// macro / thumbnail / overview) to w, using caller-supplied series-shared UIDs
// and instance number — the surgical-edit entry point (no full-pyramid write).
// It reuses writeAssociated, which encapsulates a tile-copyable codec verbatim or
// stores a decoded image as native RGB, and sets the SlideLabel module for
// label/overview.
func WriteAssociatedInstance(w io.Writer, src source.Source, a source.AssociatedImage, shared SharedUIDs, instanceNumber int) error {
	return writeAssociated(w, src, a, shared, instanceNumber)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/dicomwriter/ -run 'TestWriteAssociated' -v`
Expected: PASS (both the new test and the existing `TestWriteAssociated`).

- [ ] **Step 5: Full-package check + commit**

Run: `go build ./... && go test ./internal/dicomwriter/ -count=1`

```bash
git add internal/dicomwriter/
git commit -m "feat(dicomwriter): export SharedUIDs + WriteAssociatedInstance"
```

---

## Task 4: Synthetic replacement `source.AssociatedImage` (RGB → native)

**Files:**
- Create: `cmd/wsitools/associated_dicom.go`
- Test: `cmd/wsitools/associated_dicom_test.go`

A replacement/rotated image is an in-memory RGB raster; wrap it as a `source.AssociatedImage` so `WriteAssociatedInstance` (and, later, IFE) can consume it via the decode path.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"image"
	"testing"

	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/source"
)

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
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestRGBAssoc -v`
Expected: FAIL — `undefined: rgbAssoc`.

- [ ] **Step 3: Implement (in associated_dicom.go)**

```go
package main

import (
	"errors"
	"image"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/source"
)

// rgbAssoc is a synthesized source.AssociatedImage over an in-memory RGB raster —
// a replacement or rotated associated image. Compression()==None routes DICOM's
// writeAssociated down the decode→native-RGB path (lossless). Bytes/Source are
// unavailable (nothing on disk).
type rgbAssoc struct {
	typ string
	img *decoder.Image // PixelFormatRGB
}

func (a *rgbAssoc) Type() string                     { return a.typ }
func (a *rgbAssoc) Size() image.Point                { return image.Point{X: a.img.Width, Y: a.img.Height} }
func (a *rgbAssoc) Compression() source.Compression  { return source.CompressionNone }
func (a *rgbAssoc) Bytes() ([]byte, error)           { return nil, errors.New("rgbAssoc: no encoded bytes") }
func (a *rgbAssoc) Decode(decoder.DecodeOptions) (*decoder.Image, error) { return a.img, nil }
func (a *rgbAssoc) Source() (opentile.AssociatedEncoding, bool) {
	return opentile.AssociatedEncoding{}, false
}
func (a *rgbAssoc) IFDOffset() (int64, bool) { return 0, false }

// imageToRGB converts a decoded Go image.Image to a tightly-packed RGB
// decoder.Image (the form rgbAssoc + the codecs expect).
func imageToRGB(img image.Image) *decoder.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	pix := make([]byte, w*h*3)
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			pix[i], pix[i+1], pix[i+2] = byte(r>>8), byte(g>>8), byte(bl>>8)
			i += 3
		}
	}
	return &decoder.Image{Width: w, Height: h, Stride: w * 3, Format: decoder.PixelFormatRGB, Pix: pix}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/wsitools/ -run TestRGBAssoc -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated_dicom.go cmd/wsitools/associated_dicom_test.go
git commit -m "feat(convert): synthetic RGB source.AssociatedImage for edits"
```

---

## Task 5: DICOM-aware output resolution

**Files:**
- Modify: `cmd/wsitools/associated.go` (`resolveAssocOutput`, add `isDICOMInput` helper)
- Test: `cmd/wsitools/associated_test.go`

A DICOM slide is a directory (or a `.dcm` in one); its edited output is a directory, so the file-based `_edited<ext>` derivation is wrong. Derive `<name>_edited/` for DICOM.

- [ ] **Step 1: Write the failing test**

```go
func TestResolveAssocOutput_DICOMDir(t *testing.T) {
	dir := t.TempDir()
	series := filepath.Join(dir, "slide")
	if err := os.MkdirAll(series, 0o755); err != nil {
		t.Fatal(err)
	}
	// A directory input with no --out derives "<name>_edited" as a dir path.
	got, err := resolveAssocOutputDICOM(series, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "slide_edited") {
		t.Errorf("got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestResolveAssocOutput_DICOMDir -v`
Expected: FAIL — `undefined: resolveAssocOutputDICOM`.

- [ ] **Step 3: Implement (in associated_dicom.go)**

```go
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveAssocOutputDICOM resolves the OUTPUT DIRECTORY for a DICOM edit
// (directory-shaped, unlike the file-based resolveAssocOutput). in may be a
// series directory or a .dcm inside one; the derived default is
// "<seriesdir>_edited". --in-place returns the series directory itself.
func resolveAssocOutputDICOM(in, out string, inPlace, overwrite bool) (string, error) {
	seriesDir := in
	if fi, err := os.Stat(in); err == nil && !fi.IsDir() {
		seriesDir = filepath.Dir(in)
	}
	if inPlace {
		if out != "" {
			return "", fmt.Errorf("--in-place and -o/--output are mutually exclusive")
		}
		return seriesDir, nil
	}
	if out == "" {
		base := strings.TrimRight(seriesDir, string(filepath.Separator))
		out = base + "_edited"
	}
	if _, err := os.Stat(out); err == nil && !overwrite {
		return "", fmt.Errorf("output %s already exists (use --force)", out)
	}
	abs, _ := filepath.Abs(out)
	absSeries, _ := filepath.Abs(seriesDir)
	if abs == absSeries {
		return "", fmt.Errorf("input and output are the same directory: %s", abs)
	}
	return out, nil
}

// isDICOMInput reports whether path opens as a DICOM source (directory series or
// a .dcm). Cheap: extension + directory-contains-.dcm check (avoids a full open).
func isDICOMInput(path string) bool {
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return strings.EqualFold(filepath.Ext(path), ".dcm")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".dcm") {
			return true
		}
	}
	return false
}
```

Then in `newAssocTypeCmd` (`associated.go:474`), the `remove`/`replace` RunE closures branch on `isDICOMInput(input)` to call `resolveAssocOutputDICOM` instead of `resolveAssocOutput`:

```go
// inside remove RunE (mirror in replace RunE):
var out string
if isDICOMInput(input) {
	out, err = resolveAssocOutputDICOM(input, rmFlags.output, rmFlags.inPlace, rmFlags.overwrite)
} else {
	out, err = resolveAssocOutput(input, rmFlags.output, rmFlags.inPlace, rmFlags.overwrite)
}
if err != nil {
	return err
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/wsitools/ -run 'TestResolveAssocOutput' -v` (existing + new pass)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated.go cmd/wsitools/associated_dicom.go cmd/wsitools/associated_test.go
git commit -m "feat(convert): directory-aware output resolution for DICOM edits"
```

---

## Task 6: `runAssociatedRemoveForDICOM` + gate + dispatch

**Files:**
- Modify: `cmd/wsitools/associated_dicom.go`, `cmd/wsitools/associated.go`
- Test: `tests/integration/assoc_dicom_test.go`

`remove` = clone the series directory minus the target instance (in-place: delete it). Level instances are copied byte-for-byte.

- [ ] **Step 1: Write the failing integration test**

```go
//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// label remove on a DICOM series drops the label instance and copies every level
// instance byte-for-byte (surgical guarantee).
func TestAssocDICOM_LabelRemove(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "dicom", "Leica-4")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("no Leica-4 DICOM fixture")
	}
	out := filepath.Join(t.TempDir(), "edited")
	if o, err := exec.Command(bin, "label", "remove", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("label remove <dicom>: %v\n%s", err, o)
	}
	// No label instance should remain, but levels must (info opens the series).
	if o, err := exec.Command(bin, "info", out).CombinedOutput(); err != nil {
		t.Fatalf("info on edited series failed: %v\n%s", err, o)
	} else if containsLabelAssoc(t, bin, out) {
		t.Errorf("label still present after remove")
	}
}
```

Add `containsLabelAssoc` (helper reading `info --json` associated list — reuse the JSON-parse idiom from `tests/integration/dicom_subsampling_test.go`; assert none has type "label").

- [ ] **Step 2: Run to verify it fails**

Run: `TMPDIR=/Volumes/Ext/tmp WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestAssocDICOM_LabelRemove -v`
Expected: FAIL — DICOM currently rejected by `gateFormat` (`ErrUnsupportedAssoc`).

- [ ] **Step 3: Implement**

In `associated_dicom.go`:

```go
import (
	"io"
	"github.com/wsilabs/wsitools/internal/dicomedit"
)

func runAssociatedRemoveForDICOM(typ, input, outDir string, fl removeFlags) error {
	seriesDir := input
	if fi, err := os.Stat(input); err == nil && !fi.IsDir() {
		seriesDir = filepath.Dir(input)
	}
	insts, err := dicomedit.ClassifyInstances(seriesDir)
	if err != nil {
		return err
	}
	target := ""
	for _, in := range insts {
		if string(in.Role) == typ {
			target = in.Path
		}
	}
	if target == "" {
		return fmt.Errorf("no %s image in DICOM series", typ)
	}
	return commitDICOMEdit(seriesDir, outDir, fl.inPlace, func(dstDir string) error {
		// Remove == copy everything except the target instance + DICOMDIR.
		return nil // handled by the skip set in commitDICOMEdit
	}, map[string]bool{target: true}, nil)
}
```

Add the shared `commitDICOMEdit` helper (clone-with-skip + optional extra file, atomic temp→rename; also drops any `DICOMDIR`):

```go
// commitDICOMEdit materializes the edited series: copy every .dcm from seriesDir
// into a temp dir EXCEPT those in skip (and any DICOMDIR), optionally run addFn
// to write new instances into the temp dir, then atomically place it at outDir
// (or back over seriesDir for in-place).
func commitDICOMEdit(seriesDir, outDir string, inPlace bool, addFn func(dstDir string) error, skip map[string]bool, _ any) error {
	parent := filepath.Dir(strings.TrimRight(outDir, string(filepath.Separator)))
	if inPlace {
		parent = filepath.Dir(strings.TrimRight(seriesDir, string(filepath.Separator)))
	}
	tmp, err := os.MkdirTemp(parent, ".wsitools-dcmedit-*")
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(seriesDir)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		full := filepath.Join(seriesDir, name)
		if skip[full] || strings.EqualFold(name, "DICOMDIR") {
			continue
		}
		if err := copyFile(full, filepath.Join(tmp, name)); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
	}
	if addFn != nil {
		if err := addFn(tmp); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
	}
	dst := outDir
	if inPlace {
		dst = seriesDir
	}
	if err := os.RemoveAll(dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if !flagQuiet {
		fmt.Printf("wsitools: edited DICOM series -> %s\n", dst)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
```

In `associated.go`: add `string(opentile.FormatDICOM)` to `assocFormatSupported` (`:63`), update the gate message, and in `runAssociatedRemoveFor` (`:164`) add a branch BEFORE the splice path:

```go
if src.Format() == string(opentile.FormatDICOM) {
	src.Close()
	return runAssociatedRemoveForDICOM(typ, input, outPath, fl)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `TMPDIR=/Volumes/Ext/tmp WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestAssocDICOM_LabelRemove -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated_dicom.go cmd/wsitools/associated.go tests/integration/assoc_dicom_test.go
git commit -m "feat(convert): DICOM label remove (surgical, pyramid untouched)"
```

---

## Task 7: `runAssociatedReplaceForDICOM`

**Files:**
- Modify: `cmd/wsitools/associated_dicom.go`, `cmd/wsitools/associated.go`
- Test: `tests/integration/assoc_dicom_test.go`

`replace` = remove the old target instance + write a new native-RGB instance carrying the series' shared UIDs.

- [ ] **Step 1: Write the failing integration test**

```go
func TestAssocDICOM_LabelReplace(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "dicom", "Leica-4")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("no Leica-4 DICOM fixture")
	}
	// A small PNG to inject.
	png := filepath.Join(t.TempDir(), "new.png")
	writeTestPNG(t, png, 300, 200) // helper: solid-color PNG
	out := filepath.Join(t.TempDir(), "edited")
	if o, err := exec.Command(bin, "label", "replace", "--image", png, "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("label replace <dicom>: %v\n%s", err, o)
	}
	// The new label must be present and decode to ~300x200.
	if !containsLabelAssoc(t, bin, out) {
		t.Errorf("label missing after replace")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `TMPDIR=/Volumes/Ext/tmp WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestAssocDICOM_LabelReplace -v`
Expected: FAIL — replace-for-DICOM not implemented (splice path errors).

- [ ] **Step 3: Implement**

```go
func runAssociatedReplaceForDICOM(typ, input, outDir string, fl replaceFlags) error {
	seriesDir := input
	if fi, err := os.Stat(input); err == nil && !fi.IsDir() {
		seriesDir = filepath.Dir(input)
	}
	insts, err := dicomedit.ClassifyInstances(seriesDir)
	if err != nil {
		return err
	}
	var levelPath, oldTarget string
	nInstances := 0
	for _, in := range insts {
		nInstances++
		if in.Role == dicomedit.RoleLevel && levelPath == "" {
			levelPath = in.Path
		}
		if string(in.Role) == typ {
			oldTarget = in.Path
		}
	}
	if levelPath == "" {
		return fmt.Errorf("DICOM series has no level instance")
	}
	shared, err := dicomedit.ReadSharedUIDs(levelPath)
	if err != nil {
		return err
	}
	// Decode the replacement image (reuses the existing helper).
	img, err := decodeReplacementImage(fl.image)
	if err != nil {
		return err
	}
	rgb := imageToRGB(img)

	// Open the source once for metadata (WriteAssociatedInstance needs src).
	src, err := source.Open(seriesDir)
	if err != nil {
		return err
	}
	defer src.Close()

	skip := map[string]bool{}
	if oldTarget != "" {
		skip[oldTarget] = true
	}
	addFn := func(dstDir string) error {
		a := &rgbAssoc{typ: typ, img: rgb}
		var buf bytes.Buffer
		if err := dicomwriter.WriteAssociatedInstance(&buf, src, a, shared, nInstances+1); err != nil {
			return fmt.Errorf("build %s instance: %w", typ, err)
		}
		return os.WriteFile(filepath.Join(dstDir, typ+".dcm"), buf.Bytes(), 0o644)
	}
	return commitDICOMEdit(seriesDir, outDir, fl.inPlace, addFn, skip, nil)
}
```

Add imports (`bytes`, `dicomwriter`, `source`). In `associated.go`, add the DICOM branch to `runAssociatedReplaceFor` (`:243`) mirroring Task 6.

- [ ] **Step 4: Run to verify it passes**

Run: `TMPDIR=/Volumes/Ext/tmp WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run 'TestAssocDICOM' -v`
Expected: PASS (remove + replace).

- [ ] **Step 5: dciodvfy gate + commit**

Run (controller / local, if `dciodvfy` available): validate every `.dcm` in the edited output — 0 errors on the new label + unchanged levels.

```bash
git add cmd/wsitools/associated_dicom.go cmd/wsitools/associated.go tests/integration/assoc_dicom_test.go
git commit -m "feat(convert): DICOM label replace (native-RGB instance, series UIDs preserved)"
```

---

# Phase 2 — IFE `remove` / `replace`

## Task 8: Edit plan through `addIFEAssociated` + `encodeAssocPNG`

**Files:**
- Modify: `cmd/wsitools/convert_ife.go`
- Test: `cmd/wsitools/convert_ife_edit_test.go` (unit)

Thread an optional edit plan so the IFE associated writer can skip a type or substitute a replacement image (encoded to PNG).

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestEncodeAssocPNG -v`
Expected: FAIL — `undefined: encodeAssocPNG`.

- [ ] **Step 3: Implement**

Extract the PNG-encode block already inside `addIFEAssociated` (`convert_ife.go:344-357`) into a helper, and add an `assocEditPlan`:

```go
type assocEditPlan struct {
	skip    string        // associated type to drop ("" = none)
	replace string        // associated type to replace ("" = none)
	repImg  *decoder.Image // RGB replacement for `replace`
}

// encodeAssocPNG encodes an RGB decoder.Image to a lossless PNG blob for an IFE
// associated image.
func encodeAssocPNG(di *decoder.Image) (blob []byte, w, h uint32, err error) {
	enc, err := pngcodec.Factory{}.NewEncoder(
		codec.LevelGeometry{TileWidth: di.Width, TileHeight: di.Height, PixelFormat: codec.PixelFormatRGB8},
		codec.Quality{})
	if err != nil {
		return nil, 0, 0, err
	}
	defer enc.Close()
	b, err := enc.EncodeTile(tightIFERGB(di), di.Width, di.Height, nil)
	if err != nil {
		return nil, 0, 0, err
	}
	return b, uint32(di.Width), uint32(di.Height), nil
}
```

Change `addIFEAssociated(w *ife.Writer, src source.Source)` → `addIFEAssociated(w *ife.Writer, src source.Source, plan assocEditPlan)`. At the top of the loop: `if strings.EqualFold(a.Type(), plan.skip) || strings.EqualFold(a.Type(), plan.replace) { ...handle... }`. For a `replace` match, encode `plan.repImg` via `encodeAssocPNG` and `AddAssociated(...ImgEncPNG...)` instead of the source image; for `skip`, `continue`. Rewrite the existing default-branch PNG code to call `encodeAssocPNG`. Update `assembleIFEMetadata` to take + forward `plan` (callers in `runConvertIFE` pass `assocEditPlan{}`).

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/wsitools/ -run TestEncodeAssocPNG -v && go build ./...`
Expected: PASS + build clean (all `assembleIFEMetadata`/`addIFEAssociated` callers updated).

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/convert_ife.go cmd/wsitools/convert_ife_edit_test.go
git commit -m "feat(convert): edit-plan hook + encodeAssocPNG in the IFE writer path"
```

---

## Task 9: `runAssociated{Remove,Replace}ForIFE` + gate + dispatch

**Files:**
- Create: `cmd/wsitools/associated_ife.go`
- Modify: `cmd/wsitools/associated.go`, `cmd/wsitools/convert_ife.go`
- Test: `tests/integration/assoc_ife_test.go`

Rebuild the IFE through the convert-IFE machinery with an edit plan; pyramid copies verbatim.

- [ ] **Step 1: Write the failing integration test**

```go
//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAssocIFE_LabelRemoveReplace(t *testing.T) {
	bin := buildOnce(t)
	svs := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(svs); err != nil {
		t.Skipf("no svs fixture")
	}
	dir := t.TempDir()
	ife := filepath.Join(dir, "slide.ife")
	if o, err := exec.Command(bin, "convert", "--to", "ife", "-f", "-o", ife, svs).CombinedOutput(); err != nil {
		t.Fatalf("make ife: %v\n%s", err, o)
	}
	out := filepath.Join(dir, "nolabel.ife")
	if o, err := exec.Command(bin, "label", "remove", "-o", out, ife).CombinedOutput(); err != nil {
		t.Fatalf("label remove <ife>: %v\n%s", err, o)
	}
	// Reopens cleanly and has no label.
	if o, err := exec.Command(bin, "info", out).CombinedOutput(); err != nil {
		t.Fatalf("info edited ife: %v\n%s", err, o)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `TMPDIR=/Volumes/Ext/tmp WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestAssocIFE -v`
Expected: FAIL — IFE rejected by `gateFormat`.

- [ ] **Step 3: Implement**

In `associated_ife.go`: `runAssociatedRemoveForIFE(typ, input, outPath string, fl removeFlags)` and `runAssociatedReplaceForIFE(typ, input, outPath string, fl replaceFlags)` that call a shared `rebuildIFEWithPlan(input, outPath string, plan assocEditPlan, overwrite bool) error`. `rebuildIFEWithPlan` mirrors `runConvertIFE` (open source, verbatim-eligible pyramid copy via the existing path, then `assembleIFEMetadata(w, src, plan)`), reusing `decodeReplacementImage`+`imageToRGB` for the replace case. Refactor `runConvertIFE`'s core into a callable that accepts `assocEditPlan` (default empty for the convert command). In `associated.go`, add `string(opentile.FormatIFE)` to `assocFormatSupported`, update the gate message, and add IFE dispatch branches to `runAssociatedRemoveFor`/`runAssociatedReplaceFor` (before the splice path), each closing `src` first.

- [ ] **Step 4: Run to verify it passes**

Run: `TMPDIR=/Volumes/Ext/tmp WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestAssocIFE -v`
Expected: PASS. Also `make ife-validate` (controller) on the edited output.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated_ife.go cmd/wsitools/associated.go cmd/wsitools/convert_ife.go tests/integration/assoc_ife_test.go
git commit -m "feat(convert): IFE label remove/replace (rebuild, pyramid verbatim)"
```

---

# Phase 3 — `label rotate`

## Task 10: `rotateRGB` helper

**Files:**
- Create: `cmd/wsitools/associated_rotate.go`
- Test: `cmd/wsitools/associated_rotate_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import "testing"

func TestRotateRGB_90SwapsDimsAndTransposes(t *testing.T) {
	// 2x1 image: pixel(0,0)=red, pixel(1,0)=green.
	pix := []byte{255, 0, 0, 0, 255, 0}
	out, ow, oh := rotateRGB(pix, 2, 1, 90)
	if ow != 1 || oh != 2 {
		t.Fatalf("dims = %dx%d, want 1x2", ow, oh)
	}
	// 90° clockwise: top row becomes right column → new(0,0) is old(0,0)=red on top.
	// new row0 = red, new row1 = green.
	if !(out[0] == 255 && out[1] == 0 && out[2] == 0) {
		t.Errorf("new(0,0) = % v, want red", out[0:3])
	}
	if !(out[3] == 0 && out[4] == 255 && out[5] == 0) {
		t.Errorf("new(0,1) = % v, want green", out[3:6])
	}
}

func TestRotateRGB_180Dims(t *testing.T) {
	pix := make([]byte, 4*3*3)
	out, ow, oh := rotateRGB(pix, 4, 3, 180)
	if ow != 4 || oh != 3 || len(out) != len(pix) {
		t.Errorf("180 dims/len wrong: %dx%d len=%d", ow, oh, len(out))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestRotateRGB -v`
Expected: FAIL — `undefined: rotateRGB`.

- [ ] **Step 3: Implement**

```go
package main

// rotateRGB rotates a tightly-packed (stride=w*3) RGB buffer clockwise by
// degrees ∈ {90,180,270}. 90/270 swap width and height. degrees==0 (or any
// other value) returns the input unchanged.
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
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/wsitools/ -run TestRotateRGB -v`
Expected: PASS. (If the 90° orientation assertion fails, the rotation direction is transposed — swap the `nx,ny` formulas between the 90 and 270 cases; the test pins clockwise.)

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated_rotate.go cmd/wsitools/associated_rotate_test.go
git commit -m "feat(convert): rotateRGB 90/180/270 helper"
```

---

## Task 11: `runAssociatedRotateFor` + `rotatableTypes`

**Files:**
- Modify: `cmd/wsitools/associated_rotate.go`, `cmd/wsitools/associated.go`
- Test: `cmd/wsitools/associated_rotate_test.go` (gate unit) + `tests/integration/assoc_rotate_test.go`

Rotate = decode the target associated image, rotate it, and route through each format's replace path preserving the source's lossless encoding.

- [ ] **Step 1: Write the failing tests**

Unit (gate):

```go
func TestRotatableTypesGate(t *testing.T) {
	if !rotatableTypes["label"] {
		t.Error("label must be rotatable")
	}
	if rotatableTypes["macro"] {
		t.Error("macro must NOT be rotatable")
	}
}
```

Integration:

```go
//go:build integration

package integration

func TestAssocRotate_DICOMLabel90(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "dicom", "Leica-4")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("no Leica-4 fixture")
	}
	out := filepath.Join(t.TempDir(), "rot")
	if o, err := exec.Command(bin, "label", "rotate", "90", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("label rotate 90 <dicom>: %v\n%s", err, o)
	}
	if !containsLabelAssoc(t, bin, out) {
		t.Errorf("label missing after rotate")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestRotatableTypesGate -v`
Expected: FAIL — `undefined: rotatableTypes`.

- [ ] **Step 3: Implement**

```go
import (
	"fmt"
	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/source"
)

// rotatableTypes gates which associated images `rotate` accepts. Label-only for
// now (orientation correction); extend by adding a type here + registering its
// subcommand in newAssocTypeCmd. macro is intentionally excluded.
var rotatableTypes = map[string]bool{"label": true}

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
	// Locate + decode the current image.
	var target source.AssociatedImage
	for _, a := range src.Associated() {
		if a.Type() == typ {
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
	rgb := tightRGBFromDecoder(di) // stride→w*3; reuse tightIFERGB or a local copy
	rot, ow, oh := rotateRGB(rgb, di.Width, di.Height, degrees)
	src.Close()

	// Route through the per-format replace path with a preloaded rotated image.
	return runAssociatedReplaceRotated(typ, input, outPath, fl, &decoder.Image{
		Width: ow, Height: oh, Stride: ow * 3, Format: decoder.PixelFormatRGB, Pix: rot,
	})
}
```

`runAssociatedReplaceRotated` is a thin variant of `runAssociatedReplaceFor` that takes a preloaded `*decoder.Image` instead of `--image`, and sets a "preserve source encoding" flag on the replace-spec builders (TIFF family → keep source compression; DICOM → native; IFE → PNG). Implementation options — either (a) add a `preImg *decoder.Image` + `preserveEnc bool` field to `replaceFlags` and teach the existing `runAssociatedReplaceFor` + per-format helpers to prefer `preImg` over decoding `--image` and to preserve encoding; or (b) a parallel path. Prefer (a) — one code path. For DICOM/IFE the replace helpers already produce native/PNG (lossless), so only the TIFF-family builders (`buildReplacementAssocSpec`/`buildReplacementStrippedSpec`/`buildReplacementIFD`) need a "match source compression" branch.

In `associated.go`, add `preImg *decoder.Image` and `preserveEnc bool` to `replaceFlags`, and in the per-format replace paths: when `preImg != nil`, skip `decodeReplacementImage` and use it; when `preserveEnc`, pass the located source image's `Compression()` to the spec builder instead of the per-type default.

- [ ] **Step 4: Run to verify it passes**

Run:
```
go test ./cmd/wsitools/ -run 'TestRotatableTypesGate|TestRotateRGB' -v
TMPDIR=/Volumes/Ext/tmp WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestAssocRotate -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated_rotate.go cmd/wsitools/associated.go tests/integration/assoc_rotate_test.go
git commit -m "feat(convert): label rotate core (type-generic, label-gated, lossless-preserving)"
```

---

## Task 12: `label rotate` CLI wiring

**Files:**
- Modify: `cmd/wsitools/associated.go` (`newAssocTypeCmd`)
- Test: `tests/integration/assoc_rotate_test.go` (a TIFF-family case, CI-fixture-backed)

- [ ] **Step 1: Write the failing integration test (SVS, always-available fixture)**

```go
func TestAssocRotate_SVSLabel90(t *testing.T) {
	bin := buildOnce(t)
	svs := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(svs); err != nil {
		t.Skipf("no svs fixture")
	}
	out := filepath.Join(t.TempDir(), "rot.svs")
	o, err := exec.Command(bin, "label", "rotate", "90", "-o", out, svs).CombinedOutput()
	// CMU-1-Small-Region has no label → expect a clean "no label" error, not a crash.
	if err == nil {
		t.Logf("rotate succeeded: %s", o)
	} else if !bytesContains(o, "no label") {
		t.Fatalf("unexpected error: %v\n%s", err, o)
	}
}
```

> Pick a label-bearing SVS fixture if CMU-1-Small-Region lacks one (check `wsitools info` / the fixture pool). The point is exercising the wired `rotate` subcommand end-to-end.

- [ ] **Step 2: Run to verify it fails**

Run: `TMPDIR=/Volumes/Ext/tmp WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestAssocRotate_SVSLabel90 -v`
Expected: FAIL — `unknown command "rotate"`.

- [ ] **Step 3: Implement**

In `newAssocTypeCmd` (`associated.go:474`), after wiring `remove`/`replace`, register `rotate` only for rotatable types:

```go
if rotatableTypes[typ] {
	rotFlags := &replaceFlags{}
	rotateCmd := &cobra.Command{
		Use:   "rotate <degrees> <slide>",
		Short: "Rotate the " + typ + " associated image (90|180|270, clockwise)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			deg, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("degrees must be 90, 180, or 270 (got %q)", args[0])
			}
			input := args[1]
			var out string
			if isDICOMInput(input) {
				out, err = resolveAssocOutputDICOM(input, rotFlags.output, rotFlags.inPlace, rotFlags.overwrite)
			} else {
				out, err = resolveAssocOutput(input, rotFlags.output, rotFlags.inPlace, rotFlags.overwrite)
			}
			if err != nil {
				return err
			}
			return runAssociatedRotateFor(typ, deg, input, out, *rotFlags)
		},
	}
	bindCommonFlags(rotateCmd, &rotFlags.assocCommonFlags)
	parent.AddCommand(rotateCmd)
}
```

- [ ] **Step 4: Run to verify it passes**

Run:
```
TMPDIR=/Volumes/Ext/tmp WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestAssocRotate -v
```
Expected: PASS (SVS + DICOM rotate cases).

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated.go tests/integration/assoc_rotate_test.go
git commit -m "feat(cli): wire label rotate {90,180,270} subcommand"
```

---

# Phase 4 — docs + full verification

## Task 13: Docs

**Files:**
- Modify: `docs/commands.md`, `docs/formats.md`, `README.md`, `docs/roadmap.md`

- [ ] **Step 1: Update the format matrices**

In `docs/formats.md` and `README.md`, change the `edit` column for **DICOM-WSI** and **IFE** from `—` to `✓`. Update `docs/formats.md` footnote ⁶ / the surrounding text: the "not editable" set is now only NDPI, Philips-TIFF, Leica SCN, and BIF.

- [ ] **Step 2: Document `rotate` + DICOM/IFE editing in commands.md**

Add a `rotate` subsection under "Associated-image editing": `wsitools label rotate {90,180,270} <slide>` (clockwise; label-only; preserves the label's lossless encoding; 90/270 swap W/H). Note DICOM edits are **surgical** (single instance; pyramid untouched; output is a directory) and IFE edits **rebuild** (pyramid verbatim, associated → PNG). Update the "Other formats … not editable" line to drop DICOM and IFE.

- [ ] **Step 3: Roadmap — move items to shipped**

In `docs/roadmap.md`, move the "Associated-image editing for DICOM and IFE" and `label rotate` backlog items into a Shipped entry with the date.

- [ ] **Step 4: Commit**

```bash
git add docs/commands.md docs/formats.md README.md docs/roadmap.md
git commit -m "docs: DICOM+IFE editing + label rotate (matrices, commands, roadmap)"
```

---

## Task 14: Full suite + finish

- [ ] **Step 1: Full unit + integration run**

```bash
TMPDIR=/Volumes/Ext/tmp WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./... -count=1
TMPDIR=/Volumes/Ext/tmp WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ ./cmd/wsitools/ -count=1 -timeout 30m
go vet ./...
```
Expected: all green.

- [ ] **Step 2: Gold gates (controller, if tools available)**

`dciodvfy` on an edited DICOM series (0 errors); `make ife-validate` on an edited IFE.

- [ ] **Step 3: Finish the branch**

Use superpowers:finishing-a-development-branch (verify tests → merge `--no-ff` to main → push per the session convention).

---

## Self-review notes (author)

- **Spec coverage:** DICOM surgical remove/replace (Tasks 1–7), IFE rebuild remove/replace (Tasks 8–9), `label rotate` type-generic + gated (Tasks 10–12), directory I/O (Task 5), DICOMDIR drop (Task 6 `commitDICOMEdit`), lossless encodings (native/PNG/preserve — Tasks 4/8/11), docs + matrices + roadmap (Task 13), tests incl. surgical byte-identity + dciodvfy/ife-validate (Tasks 6/7/9/14). All spec sections mapped.
- **Type consistency:** `SharedUIDs` (Task 3) used by Tasks 2/7; `rgbAssoc`/`imageToRGB` (Task 4) used by 7/11; `assocEditPlan`/`encodeAssocPNG` (Task 8) used by 9; `rotateRGB` (10) used by 11; `rotatableTypes` (11) used by 12; `resolveAssocOutputDICOM`/`isDICOMInput` (5) used by 6/7/12.
- **Known follow-through for the implementer:** (a) confirm the `WSILabs/dicom` stop-before-pixels API name (Task 1 note); (b) the `tightRGBFromDecoder` helper in Task 11 = the same stride-tightening as `tightIFERGB`/`tightRGB` — reuse one, don't duplicate; (c) `runAssociatedReplaceRotated` is specified as the `preImg`+`preserveEnc` path on the existing replace code (Task 11 option a), not a fork; (d) pick a label-bearing fixture for the SVS rotate test if CMU-1-Small-Region has none.
