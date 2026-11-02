package certificate

import (
	"bytes"
	"os"
	"testing"
)

func loadTemplate(t testing.TB, path string) Template {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("template fixture unavailable: %v", err)
	}
	return Template{Filename: path, Content: content}
}

func TestGenerateFromJPGAndPDF(t *testing.T) {
	for _, path := range []string{"../../assests/template.jpg", "../../assests/46-certificate-Ada-Lovelace.pdf"} {
		cfg := Config{Template: loadTemplate(t, path), NameY: 60, NameSize: 24, NumberX: 15, NumberY: 15, NumberSize: 10}
		content, filename, err := Generate("Grace Hopper", "AI4ALL-2026-0042", cfg)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !bytes.HasPrefix(content, []byte("%PDF-")) {
			t.Fatalf("%s: output is not a PDF", path)
		}
		if filename != "certificate-Grace-Hopper.pdf" {
			t.Fatalf("filename = %q", filename)
		}
	}
}

func TestGenerateWithoutTemplate(t *testing.T) {
	if _, _, err := Generate("Ada", "", Config{}); err == nil {
		t.Fatal("expected an error without a template")
	}
}

func BenchmarkGenerateJPG(b *testing.B) {
	cfg := Config{Template: loadTemplate(b, "../../assests/template.jpg"), NameY: 60, NameSize: 24}
	for i := 0; i < b.N; i++ {
		if _, _, err := Generate("Ada Lovelace", "AI4ALL-2026-0001", cfg); err != nil {
			b.Fatal(err)
		}
	}
}
