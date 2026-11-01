package certificate

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"github.com/phpdave11/gofpdf"
)

// Template is the certificate background held in memory, so rendering touches
// neither the local disk nor a shared volume.
type Template struct {
	Filename string
	Content  []byte
}

func (t Template) IsPDF() bool {
	return strings.EqualFold(filepath.Ext(t.Filename), ".pdf")
}

func (t Template) ContentType() string {
	if t.IsPDF() {
		return "application/pdf"
	}
	return "image/jpeg"
}

type Config struct {
	Template Template
	NameX    float64
	NameY    float64
	NameSize float64
}

func Generate(studentName string, cfg Config) ([]byte, string, error) {
	if len(cfg.Template.Content) == 0 {
		return nil, "", errors.New("certificate template is not configured")
	}
	var content []byte
	var err error
	if cfg.Template.IsPDF() {
		content, err = generateFromPDF(studentName, cfg)
	} else {
		content, err = generateFromJPG(studentName, cfg)
	}
	if err != nil {
		return nil, "", err
	}
	return content, fmt.Sprintf("certificate-%s.pdf", safeFilename(studentName)), nil
}

func generateFromJPG(studentName string, cfg Config) ([]byte, error) {
	pdf := gofpdf.New("L", "mm", "A4", "")
	options := gofpdf.ImageOptions{ImageType: "JPG"}
	pdf.RegisterImageOptionsReader("template", options, bytes.NewReader(cfg.Template.Content))
	pdf.AddPage()
	pdf.ImageOptions("template", 0, 0, 297, 210, false, options, 0, "")
	pdf.SetFont("Helvetica", "B", cfg.NameSize)
	pdf.SetTextColor(25, 45, 80)
	pdf.SetXY(cfg.NameX, cfg.NameY)
	pdf.CellFormat(297-cfg.NameX, 10, studentName, "", 0, "C", false, 0, "")
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func generateFromPDF(studentName string, cfg Config) ([]byte, error) {
	offsetY := 105 - cfg.NameY
	desc := fmt.Sprintf("font:Helvetica, points:%d, scale:1 abs, pos:c, off:%.2f %.2f, rot:0",
		int(cfg.NameSize), cfg.NameX, offsetY)
	watermark, err := api.TextWatermark(studentName, desc, true, false, types.MILLIMETRES)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := api.AddWatermarks(bytes.NewReader(cfg.Template.Content), &buf, nil, watermark, model.NewDefaultConfiguration()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func safeFilename(value string) string {
	result := make([]rune, 0, len(value))
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			result = append(result, char)
		} else {
			result = append(result, '-')
		}
	}
	return string(result)
}
