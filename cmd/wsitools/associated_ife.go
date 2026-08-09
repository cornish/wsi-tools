package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	opentile "github.com/wsilabs/opentile-go"

	"github.com/wsilabs/wsitools/internal/ife"
	"github.com/wsilabs/wsitools/internal/source"
)

// runAssociatedRemoveForIFE removes an associated image from an IFE slide via
// a pure verbatim pyramid rebuild. The pyramid tiles are copied byte-for-byte;
// only the associated set in the METADATA block changes.
func runAssociatedRemoveForIFE(typ, input, outPath string, fl removeFlags) error {
	// Check that the type exists before rebuilding (match other formats' behavior).
	src, err := source.Open(input)
	if err != nil {
		return err
	}
	lower := strings.ToLower(typ)
	found := false
	for _, a := range src.Associated() {
		if strings.ToLower(a.Type()) == lower {
			found = true
			break
		}
	}
	src.Close()
	if !found {
		return fmt.Errorf("no %s image in slide", typ)
	}
	return rebuildIFEWithPlan(input, outPath, ifeEditPlan{skip: lower})
}

// runAssociatedReplaceForIFE replaces (or adds) an associated image in an IFE
// slide via a pure verbatim pyramid rebuild.
func runAssociatedReplaceForIFE(typ, input, outPath string, fl replaceFlags) error {
	img, err := decodeReplacementImage(fl.image)
	if err != nil {
		return err
	}
	lower := strings.ToLower(typ)
	return rebuildIFEWithPlan(input, outPath, ifeEditPlan{replace: lower, repImg: imageToRGB(img)})
}

// rebuildIFEWithPlan opens the input IFE, copies the pyramid tiles verbatim into
// a new IFE at outPath, and writes metadata with the given edit plan applied.
// Requires the source to be verbatim-eligible (256px JPEG/AVIF-tiled, non-
// overlapping). Output is written atomically: tiles go to a temp file in the same
// directory, then renamed over outPath.
func rebuildIFEWithPlan(input, outPath string, plan ifeEditPlan) error {
	slide, err := opentile.OpenFile(input)
	if err != nil {
		return fmt.Errorf("open ife: %w", err)
	}
	defer slide.Close()

	src := source.FromSlide(slide, input)

	// Verbatim eligibility: must be 256px JPEG/AVIF-tiled (always true for a
	// wsitools-written IFE, but guard explicitly for safety).
	if !ifeVerbatimEligible(src, "", 1, 0, false) {
		return fmt.Errorf("IFE edit requires a 256px JPEG/AVIF-tiled source (source is not verbatim-eligible)")
	}

	// Determine encoding from source L0 codec.
	srcLevels := src.Levels()
	var srcCodecName string
	switch srcLevels[0].Compression() {
	case source.CompressionAVIF:
		srcCodecName = "avif"
	default:
		srcCodecName = "jpeg"
	}
	encByte, ok := ife.EncodingFor(srcCodecName)
	if !ok {
		return fmt.Errorf("ife rebuild: source codec %q not carriable by IFE", srcCodecName)
	}

	l0 := srcLevels[0].Size()
	md := slide.Metadata()

	// Write to a temp file in the same directory so the final rename is atomic.
	dir := filepath.Dir(outPath)
	tmp, err := os.CreateTemp(dir, ".ife-edit-*.tmp")
	if err != nil {
		return fmt.Errorf("ife rebuild: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close() // ife.Create will open it by path

	w, err := ife.Create(tmpPath, ife.Options{
		Encoding:      encByte,
		XExtent:       uint32(l0.X),
		YExtent:       uint32(l0.Y),
		MPP:           md.MPP.X,
		Magnification: md.Magnification,
	})
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("create ife: %w", err)
	}

	// Copy pyramid tiles verbatim (byte-identical).
	if err := writeIFEVerbatim(w, src); err != nil {
		w.Abort()
		os.Remove(tmpPath)
		return err
	}

	// Metadata sub-blocks with plan applied (skip/replace one associated type).
	assembleIFEMetadata(w, src, plan)

	if err := w.Finalize(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("finalize ife: %w", err)
	}

	// Atomic rename.
	if err := os.Rename(tmpPath, outPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("ife rebuild: rename temp to output: %w", err)
	}

	if !flagQuiet {
		verb := "removed"
		typ := plan.skip
		if plan.replace != "" {
			verb = "replaced"
			typ = plan.replace
		}
		fmt.Printf("wsitools: %s %s: %s -> %s\n", verb, typ, input, outPath)
	}
	return nil
}
