package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/dicomedit"
	dicomwriter "github.com/wsilabs/wsitools/internal/dicomwriter"
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

func (a *rgbAssoc) Type() string      { return a.typ }
func (a *rgbAssoc) Size() image.Point { return image.Point{X: a.img.Width, Y: a.img.Height} }
func (a *rgbAssoc) Compression() source.Compression {
	return source.CompressionNone
}
func (a *rgbAssoc) Bytes() ([]byte, error) { return nil, errors.New("rgbAssoc: no encoded bytes") }
func (a *rgbAssoc) Decode(decoder.DecodeOptions) (*decoder.Image, error) {
	return a.img, nil
}
func (a *rgbAssoc) Source() (opentile.AssociatedEncoding, bool) {
	return opentile.AssociatedEncoding{}, false
}
func (a *rgbAssoc) IFDOffset() (int64, bool) { return 0, false }

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
	abs, _ := filepath.Abs(out)
	if _, err := os.Stat(abs); err == nil && !overwrite {
		return "", fmt.Errorf("output %s already exists (use --force)", abs)
	}
	absSeries, _ := filepath.Abs(seriesDir)
	if abs == absSeries {
		return "", fmt.Errorf("input and output are the same directory: %s", abs)
	}
	return abs, nil
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

// runAssociatedRemoveForDICOM removes the associated instance of the given type
// from a DICOM series directory. All other .dcm files are copied byte-for-byte
// into outDir (surgical guarantee: pyramid/level instances are untouched).
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
	return commitDICOMEdit(seriesDir, outDir, fl.inPlace, nil, map[string]bool{target: true})
}

// runAssociatedReplaceForDICOM replaces (or adds) the associated instance of the
// given type in a DICOM series. If a matching instance already exists it is
// dropped; a new native-RGB instance carrying the series' shared UIDs is written
// in its place. When no existing instance matches, the new instance is simply
// appended (add-new semantics).
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
	img, err := decodeReplacementImage(fl.image)
	if err != nil {
		return err
	}
	rgb := imageToRGB(img)

	src, err := source.Open(seriesDir)
	if err != nil {
		return err
	}
	defer src.Close()

	skip := map[string]bool{}
	if oldTarget != "" {
		skip[oldTarget] = true
	}
	instNum := nInstances + 1 // add-new: final count is nInstances+1
	if oldTarget != "" {
		instNum = nInstances // replace: one dropped, one added → final count stays nInstances
	}
	addFn := func(dstDir string) error {
		a := &rgbAssoc{typ: typ, img: rgb}
		var buf bytes.Buffer
		if err := dicomwriter.WriteAssociatedInstance(&buf, src, a, shared, instNum); err != nil {
			return fmt.Errorf("build %s instance: %w", typ, err)
		}
		return os.WriteFile(filepath.Join(dstDir, typ+".dcm"), buf.Bytes(), 0o644)
	}
	return commitDICOMEdit(seriesDir, outDir, fl.inPlace, addFn, skip)
}

// commitDICOMEdit materializes the edited series: copy every .dcm from seriesDir
// into a temp dir EXCEPT those in skip (and any DICOMDIR), optionally run addFn
// to write new instances into the temp dir, then atomically place it at outDir
// (or back over seriesDir for in-place).
//
// The temp dir is created as a sibling of the destination so os.Rename is
// same-filesystem and atomic. For --in-place (base==seriesDir) we RemoveAll
// the original then Rename the temp into its place — the temp is a sibling, not
// inside seriesDir, so removal is safe.
func commitDICOMEdit(seriesDir, outDir string, inPlace bool, addFn func(dstDir string) error, skip map[string]bool) error {
	base := outDir
	if inPlace {
		base = seriesDir
	}
	parent := filepath.Dir(strings.TrimRight(base, string(filepath.Separator)))
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
		if err := dcmCopyFile(full, filepath.Join(tmp, name)); err != nil {
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
	dst := base
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

// dcmCopyFile copies src to dst with 0o644 permissions.
func dcmCopyFile(src, dst string) error {
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
