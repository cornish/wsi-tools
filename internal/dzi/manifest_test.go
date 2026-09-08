package dzi

import (
	"bytes"
	"strings"
	"testing"
)

func TestManifestBytes(t *testing.T) {
	m := Manifest{Format: "jpeg", Overlap: 1, TileSize: 256, Width: 2220, Height: 2967}
	var buf bytes.Buffer
	if err := m.Write(&buf); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`xmlns="http://schemas.microsoft.com/deepzoom/2008"`,
		`Format="jpeg"`,
		`Overlap="1"`,
		`TileSize="256"`,
		`Width="2220"`,
		`Height="2967"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q\nfull output:\n%s", want, s)
		}
	}
}

func TestManifestPNGFormat(t *testing.T) {
	m := Manifest{Format: "png", Overlap: 0, TileSize: 512, Width: 1024, Height: 768}
	var buf bytes.Buffer
	if err := m.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `Format="png"`) {
		t.Errorf("png format not preserved")
	}
}

// TestManifestScaleMetadata: when MPP/magnification are set, the manifest carries
// them as namespaced wsi:* attributes on <Image> (opentile-go#113), alongside the
// wsi namespace declaration. Standard DeepZoom viewers ignore unknown attributes.
func TestManifestScaleMetadata(t *testing.T) {
	m := Manifest{
		Format: "jpeg", Overlap: 1, TileSize: 254, Width: 46000, Height: 32914,
		MPP: 0.499, MPPX: 0.499, MPPY: 0.5, Magnification: 20,
	}
	var buf bytes.Buffer
	if err := m.Write(&buf); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, want := range []string{
		`xmlns:wsi="https://github.com/wsilabs/wsitools/dzi/2026"`,
		`wsi:MPP="0.499"`,
		`wsi:MPPX="0.499"`,
		`wsi:MPPY="0.5"`,
		`wsi:Magnification="20"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q\nfull output:\n%s", want, s)
		}
	}
}

// TestManifestNoScaleMetadata: with no scale metadata, the manifest is the plain
// DeepZoom document — no wsi namespace or attributes (byte-compatible with pre-#29
// output for viewers).
func TestManifestNoScaleMetadata(t *testing.T) {
	m := Manifest{Format: "jpeg", Overlap: 1, TileSize: 256, Width: 2220, Height: 2967}
	var buf bytes.Buffer
	if err := m.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if s := buf.String(); strings.Contains(s, "wsi:") || strings.Contains(s, "xmlns:wsi") {
		t.Errorf("expected no wsi metadata when unset, got:\n%s", s)
	}
}
