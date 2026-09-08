package dzi

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
)

// wsiNamespace is the namespace for wsitools' DeepZoom scale-metadata extension
// attributes (opentile-go#113 / wsitools#29). Standard DeepZoom XML has no slot
// for MPP/magnification; these namespaced attributes on <Image> carry them
// without breaking viewers, which ignore unknown attributes.
const wsiNamespace = "https://github.com/wsilabs/wsitools/dzi/2026"

// Manifest is the in-memory representation of a DZI .dzi manifest.
// The writer emits the canonical Microsoft DeepZoom 2008 namespace.
type Manifest struct {
	Format   string // "jpeg" or "png"
	Overlap  int
	TileSize int
	Width    int
	Height   int

	// Scale metadata (optional). Emitted as namespaced wsi:* attributes on
	// <Image> only when non-zero. MPP is the symmetric micrometres/pixel; MPPX/
	// MPPY are the per-axis values; Magnification is the objective power.
	MPP           float64
	MPPX          float64
	MPPY          float64
	Magnification float64
}

type manifestXML struct {
	XMLName  xml.Name `xml:"Image"`
	XMLNS    string   `xml:"xmlns,attr"`
	WSINS    string   `xml:"xmlns:wsi,attr,omitempty"`
	Format   string   `xml:"Format,attr"`
	Overlap  int      `xml:"Overlap,attr"`
	TileSize int      `xml:"TileSize,attr"`
	// wsi:* scale metadata — string-typed with omitempty so a zero value emits
	// nothing (keeps the manifest byte-identical to the pre-#29 form when unset).
	MPP  string  `xml:"wsi:MPP,attr,omitempty"`
	MPPX string  `xml:"wsi:MPPX,attr,omitempty"`
	MPPY string  `xml:"wsi:MPPY,attr,omitempty"`
	Mag  string  `xml:"wsi:Magnification,attr,omitempty"`
	Size sizeXML `xml:"Size"`
}

type sizeXML struct {
	Width  int `xml:"Width,attr"`
	Height int `xml:"Height,attr"`
}

// fmtScale renders a scale value for a wsi:* attribute, or "" (omitted) when the
// value is zero or non-finite. %g matches the compact form used elsewhere.
func fmtScale(v float64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// Write emits the manifest as a UTF-8 XML document. Named Write (not
// WriteTo) to avoid clashing with io.WriterTo's (int64, error) shape.
func (m Manifest) Write(w io.Writer) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	doc := manifestXML{
		XMLNS:    "http://schemas.microsoft.com/deepzoom/2008",
		Format:   m.Format,
		Overlap:  m.Overlap,
		TileSize: m.TileSize,
		MPP:      fmtScale(m.MPP),
		MPPX:     fmtScale(m.MPPX),
		MPPY:     fmtScale(m.MPPY),
		Mag:      fmtScale(m.Magnification),
		Size:     sizeXML{Width: m.Width, Height: m.Height},
	}
	if doc.MPP != "" || doc.MPPX != "" || doc.MPPY != "" || doc.Mag != "" {
		doc.WSINS = wsiNamespace
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("dzi: encode manifest: %w", err)
	}
	return enc.Flush()
}
