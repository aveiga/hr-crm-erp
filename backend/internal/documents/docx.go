// Package documents fills Word templates and converts them to PDF, reproducing
// the Offer and IGTR flows that the original Excel VBA performed with Word COM
// automation — but headless and on Linux.
//
// Two template forms are supported:
//   - a *.xml body fragment (the contents of word/document.xml) that we wrap
//     into a valid .docx; this is what ships in templates/ so generation works
//     out of the box and the layout is easy to edit in a text editor.
//   - a real *.docx (drop your finished Word file in templates/ and it is used
//     verbatim). Placeholders use {{Field}} for scalars and a table row
//     containing {{line.<col>}} placeholders that is cloned per line item.
package documents

import (
	"archive/zip"
	"bytes"
	"embed"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// builtinTemplates holds the default Offer/IGTR templates compiled into the
// binary so document generation works without any filesystem setup.
//
//go:embed templates/*.xml
var builtinTemplates embed.FS

// renderBody applies scalar and line-item substitutions to a word/document.xml
// body. Scalars: {{Key}}. Line items: the single <w:tr> containing any
// {{line.col}} placeholder is duplicated once per line.
func renderBody(body string, values map[string]string, lines []map[string]string) string {
	return renderBodyRepeats(body, values, map[string][]map[string]string{"line": lines})
}

// renderBodyRepeats is renderBody for any number of repeating row groups,
// keyed by placeholder prefix: "line" fills the {{line.*}} row, "schedule" the
// {{schedule.*}} row, and so on. Each group's template row is cloned per row.
func renderBodyRepeats(body string, values map[string]string, repeats map[string][]map[string]string) string {
	body = normalizePlaceholders(body)
	for prefix, rows := range repeats {
		body = expandRows(body, prefix, rows)
	}
	return replaceScalars(body, values)
}

// placeholderRe matches a {{...}} token that may have been split across runs by
// Word (i.e. with XML tags interspersed inside it).
var placeholderRe = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
var tagRe = regexp.MustCompile(`<[^>]+>`)

// normalizePlaceholders makes split placeholders contiguous. Word often breaks
// "{{Field}}" across several <w:r>/<w:t> runs; we strip the XML tags *inside*
// each placeholder so the literal token can be matched and replaced. This lets
// users upload ordinary Word documents without hand-crafting single-run fields.
func normalizePlaceholders(body string) string {
	return placeholderRe.ReplaceAllStringFunc(body, func(m string) string {
		return tagRe.ReplaceAllString(m, "")
	})
}

var trRe = regexp.MustCompile(`(?s)<w:tr\b.*?</w:tr>`)
var rowTokenRe = regexp.MustCompile(`\{\{([a-zA-Z0-9_]+)\.([a-zA-Z0-9_]+)\}\}`)

// expandRows clones the first <w:tr> containing a {{prefix.*}} token once per
// row, filling that row's tokens. Other placeholders in the row are left for
// the scalar pass.
func expandRows(body, prefix string, rows []map[string]string) string {
	marker := "{{" + prefix + "."
	for _, r := range trRe.FindAllStringIndex(body, -1) {
		row := body[r[0]:r[1]]
		if !strings.Contains(row, marker) {
			continue
		}
		var b strings.Builder
		for _, ln := range rows {
			b.WriteString(rowTokenRe.ReplaceAllStringFunc(row, func(tok string) string {
				m := rowTokenRe.FindStringSubmatch(tok)
				if m[1] != prefix {
					return tok
				}
				return xmlEscape(ln[m[2]])
			}))
		}
		return body[:r[0]] + b.String() + body[r[1]:]
	}
	return body
}

// scalarRe matches any {{...}} placeholder left after row expansion.
var scalarRe = regexp.MustCompile(`\{\{([a-zA-Z0-9_.]+)\}\}`)

// replaceScalars fills every placeholder in one pass; unknown ones (including
// repeating-row tokens with no rows) are blanked so they don't show.
func replaceScalars(body string, values map[string]string) string {
	return scalarRe.ReplaceAllStringFunc(body, func(tok string) string {
		return xmlEscape(values[scalarRe.FindStringSubmatch(tok)[1]])
	})
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// assembleDocx wraps a word/document.xml body into a minimal but valid .docx.
func assembleDocx(documentXML string) ([]byte, error) {
	files := map[string]string{
		"[Content_Types].xml":          contentTypesXML,
		"_rels/.rels":                  relsXML,
		"word/document.xml":            documentXML,
		"word/styles.xml":              stylesXML,
		"word/_rels/document.xml.rels": docRelsXML,
	}
	return zipFiles(files)
}

// renderDocxZip rewrites word/document.xml inside an existing .docx, preserving
// all other parts (styles, headers, fonts, images).
func renderDocxZip(src []byte, values map[string]string, lines []map[string]string) ([]byte, error) {
	return renderDocxZipRepeats(src, values, map[string][]map[string]string{"line": lines})
}

// renderDocxZipRepeats is renderDocxZip with any number of repeating row
// groups (see renderBodyRepeats). Headers and footers are filled too.
func renderDocxZipRepeats(src []byte, values map[string]string, repeats map[string][]map[string]string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		return nil, fmt.Errorf("read docx: %w", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		if f.Name == "word/document.xml" || isHeaderFooter(f.Name) {
			data = []byte(renderBodyRepeats(string(data), values, repeats))
		}
		fw, err := zw.Create(f.Name)
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func isHeaderFooter(name string) bool {
	return strings.HasPrefix(name, "word/header") || strings.HasPrefix(name, "word/footer")
}

func zipFiles(files map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		fw, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// --- minimal docx boilerplate ----------------------------------------------

const contentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
</Types>`

const relsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

const docRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`

const stylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:cs="Arial"/><w:sz w:val="20"/></w:rPr></w:rPrDefault></w:docDefaults>
</w:styles>`
