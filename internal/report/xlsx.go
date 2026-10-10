package report

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// XLSX renders the run as an Excel workbook with the sheets Summary, Device, Test cases,
// Steps and Conformance. It writes the minimal SpreadsheetML package by hand: inline strings,
// a bold header row frozen in place, and fixed column widths.
func XLSX(r *Run) ([]byte, error) {
	sheets := []table{summaryTable(r), deviceTable(r), caseTable(r), stepTable(r), conformanceTable(r)}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := []struct{ name, content string }{
		{"[Content_Types].xml", contentTypes(len(sheets))},
		{"_rels/.rels", rootRels},
		{"docProps/core.xml", coreProps(r)},
		{"xl/workbook.xml", workbook(sheets)},
		{"xl/_rels/workbook.xml.rels", workbookRels(len(sheets))},
		{"xl/styles.xml", styles},
	}
	for i, t := range sheets {
		files = append(files, struct{ name, content string }{fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), worksheet(t)})
	}
	var err error
	for _, f := range files {
		if err == nil {
			err = addZipFile(zw, f.name, f.content)
		}
	}
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	return buf.Bytes(), err
}

func addZipFile(zw *zip.Writer, name, content string) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err == nil {
		_, err = w.Write([]byte(content))
	}
	return err
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

func contentTypes(sheets int) string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	b.WriteString(`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	b.WriteString(`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`)
	for i := 1; i <= sheets; i++ {
		fmt.Fprintf(&b, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i)
	}
	b.WriteString(`</Types>`)
	return b.String()
}

const rootRels = xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
	`</Relationships>`

func coreProps(r *Run) string {
	return xmlHeader + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" ` +
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + xmlText("EEBUS test report "+r.ID) + `</dc:title>` +
		`<dc:creator>eebus-testbench ` + xmlText(r.Testbench.Version) + `</dc:creator>` +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + r.StartedAt.UTC().Format("2006-01-02T15:04:05Z") + `</dcterms:created>` +
		`</cp:coreProperties>`
}

func workbook(sheets []table) string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	for i, t := range sheets {
		fmt.Fprintf(&b, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlText(sheetName(t.Name)), i+1, i+1)
	}
	b.WriteString(`</sheets></workbook>`)
	return b.String()
}

func workbookRels(sheets int) string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= sheets; i++ {
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i, i)
	}
	fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, sheets+1)
	b.WriteString(`</Relationships>`)
	return b.String()
}

// styles defines cell format 1 (bold header on a light fill) and 2 (wrapped text, top
// aligned).
const styles = xmlHeader + `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
	`<fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill>` +
	`<fill><patternFill patternType="solid"><fgColor rgb="FFE8EEF4"/><bgColor indexed="64"/></patternFill></fill></fills>` +
	`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="3"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
	`<xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1"/>` +
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf></cellXfs>` +
	`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>` +
	`</styleSheet>`

func worksheet(t table) string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	b.WriteString(`<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`)
	if len(t.Widths) > 0 {
		b.WriteString(`<cols>`)
		for i, w := range t.Widths {
			fmt.Fprintf(&b, `<col min="%d" max="%d" width="%g" customWidth="1"/>`, i+1, i+1, w)
		}
		b.WriteString(`</cols>`)
	}
	b.WriteString(`<sheetData>`)
	header := make([]any, len(t.Header))
	for i, h := range t.Header {
		header[i] = h
	}
	writeRow(&b, 1, header, 1)
	for i, row := range t.Rows {
		writeRow(&b, i+2, row, 2)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

func writeRow(b *strings.Builder, index int, cells []any, style int) {
	fmt.Fprintf(b, `<row r="%d">`, index)
	for col, cell := range cells {
		ref := columnName(col) + strconv.Itoa(index)
		switch v := cell.(type) {
		case float64:
			fmt.Fprintf(b, `<c r="%s" s="%d"><v>%s</v></c>`, ref, style, strconv.FormatFloat(v, 'f', -1, 64))
		default:
			text := fmt.Sprint(v)
			if text != "" {
				fmt.Fprintf(b, `<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, xmlText(limitCell(text)))
			}
		}
	}
	b.WriteString(`</row>`)
}

// columnName turns a zero-based column index into its spreadsheet letters: 0 is A, 26 is AA.
func columnName(index int) string {
	name := ""
	for n := index + 1; n > 0; n = (n - 1) / 26 {
		name = string(rune('A'+(n-1)%26)) + name
	}
	return name
}

// limitCell keeps a cell under the 32767 characters a spreadsheet cell holds.
func limitCell(s string) string {
	const limit = 32000
	if utf8.RuneCountInString(s) > limit {
		runes := []rune(s)
		s = string(runes[:limit]) + " ..."
	}
	return s
}

// sheetName keeps a sheet name within the 31 characters and the character set Excel allows.
func sheetName(s string) string {
	replacer := strings.NewReplacer("[", "(", "]", ")", ":", "-", "*", "-", "?", "", "/", "-", "\\", "-")
	s = replacer.Replace(s)
	if len(s) > 31 {
		s = s[:31]
	}
	return s
}

// xmlText escapes element text and drops characters XML 1.0 cannot carry.
func xmlText(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == '&':
			b.WriteString("&amp;")
		case c == '<':
			b.WriteString("&lt;")
		case c == '>':
			b.WriteString("&gt;")
		case c == '\t' || c == '\n' || c == '\r' || c >= 0x20 && c != 0xFFFE && c != 0xFFFF:
			b.WriteRune(c)
		}
	}
	return b.String()
}
