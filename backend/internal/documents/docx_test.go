package documents

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestRenderBodyScalarsAndLines(t *testing.T) {
	body := `<w:body>
<w:p><w:t>{{Title}}</w:t></w:p>
<w:tbl>
<w:tr><w:tc><w:t>Description</w:t></w:tc><w:tc><w:t>Net</w:t></w:tc></w:tr>
<w:tr><w:tc><w:t>{{line.description}}</w:t></w:tc><w:tc><w:t>{{line.line_net}}</w:t></w:tc></w:tr>
</w:tbl>
<w:p><w:t>{{Leftover}}</w:t></w:p>
</w:body>`

	out := renderBody(body,
		map[string]string{"Title": "Invoice & Co <X>"},
		[]map[string]string{
			{"description": "Row A", "line_net": "100.00"},
			{"description": "Row B", "line_net": "250.00"},
		},
	)

	if !strings.Contains(out, "Invoice &amp; Co &lt;X&gt;") {
		t.Fatalf("scalar not escaped/substituted: %s", out)
	}
	for _, want := range []string{"Row A", "Row B", "100.00", "250.00"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing line value %q in %s", want, out)
		}
	}
	// Header row must remain exactly once.
	if strings.Count(out, "Description") != 1 {
		t.Fatalf("header row duplicated/lost: %d", strings.Count(out, "Description"))
	}
	// Leftover placeholder blanked.
	if strings.Contains(out, "{{") {
		t.Fatalf("placeholders remain: %s", out)
	}
}

func TestAssembleDocxIsValidZip(t *testing.T) {
	body := `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Hi</w:t></w:r></w:p></w:body></w:document>`
	out, err := assembleDocx(body)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	required := map[string]bool{"[Content_Types].xml": false, "_rels/.rels": false, "word/document.xml": false}
	for _, f := range zr.File {
		if _, ok := required[f.Name]; ok {
			required[f.Name] = true
			if f.Name == "word/document.xml" {
				rc, _ := f.Open()
				b, _ := io.ReadAll(rc)
				rc.Close()
				if !strings.Contains(string(b), "Hi") {
					t.Fatalf("document.xml content lost")
				}
			}
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("docx missing required part %s", name)
		}
	}
}

func TestRenderDocxZipRewritesDocument(t *testing.T) {
	src, err := assembleDocx(`<w:document><w:body><w:p><w:t>{{Greeting}}</w:t></w:p></w:body></w:document>`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderDocxZip(src, map[string]string{"Greeting": "Hello"}, nil)
	if err != nil {
		t.Fatalf("render docx zip: %v", err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			if !strings.Contains(string(b), "Hello") {
				t.Fatalf("expected rewritten content, got %s", b)
			}
			return
		}
	}
	t.Fatal("document.xml missing")
}
