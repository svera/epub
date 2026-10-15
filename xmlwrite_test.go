package epub

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
)

func TestWritePreservesXMLTree(t *testing.T) {
	pkg := `<?xml version="1.0" encoding="UTF-8"?>
<?vendor keep?>
<p:package xmlns:p="http://www.idpf.org/2007/opf" xmlns:d="http://purl.org/dc/elements/1.1/" version="3.0">
  <p:metadata>
    <!-- metadata comment -->
    <d:title id="title">Old title</d:title>
    <v:extension xmlns:v="urn:vendor" v:flag="keep"><![CDATA[<raw> & content]]><v:child/></v:extension>
  </p:metadata>
  <p:manifest>
    <!-- manifest comment -->
    <p:item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
  </p:manifest>
</p:package>
<!-- trailing comment -->
`
	out, err := updatePackageMetadata([]byte(pkg), &Information{
		Title: []MetadataValue{NewMetadataValue("New title")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`<?vendor keep?>`,
		`<!-- metadata comment -->`,
		`<!-- manifest comment -->`,
		`<!-- trailing comment -->`,
		`<v:extension xmlns:v="urn:vendor" v:flag="keep"><![CDATA[<raw> & content]]><v:child/></v:extension>`,
		`<d:title id="title">New title</d:title>`,
		pkg[strings.Index(pkg, "  <p:manifest>"):],
	} {
		if !bytes.Contains(out, []byte(fragment)) {
			t.Fatalf("XML tree lost %q:\n%s", fragment, out)
		}
	}
}

func TestWriteConvertsXMLToUTF8(t *testing.T) {
	pkg := `<?xml version='1.0' encoding='ISO-8859-1' standalone='yes'?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="2.0">
<metadata><dc:title>Caf` + "\xe9" + `</dc:title></metadata></package>`
	out, err := updatePackageMetadata([]byte(pkg), &Information{
		Publisher: []MetadataValue{NewMetadataValue("Publisher")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`encoding="UTF-8"`)) || !bytes.Contains(out, []byte(`standalone='yes'`)) {
		t.Fatalf("incorrect XML declaration: %s", out)
	}
	doc, err := newPackageDocument(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Metadata.Title[0].Value != "Caf\u00e9" {
		t.Fatalf("non-UTF-8 content was corrupted: %s", out)
	}
}

func TestWritePreservesChildAttributeNamespaces(t *testing.T) {
	pkg := `<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
<metadata><dc:title xmlns:v="urn:local" v:code="ID" xml:lang="es">Title</dc:title></metadata></package>`
	out, err := updatePackageMetadata([]byte(pkg), &Information{
		Title: []MetadataValue{{Refinements: []Meta{}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := newPackageDocument(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	title := doc.Metadata.Title[0]
	if title.Lang != "es" || len(title.Attributes) != 1 ||
		title.Attributes[0].Name != (xml.Name{Space: "urn:local", Local: "code"}) {
		t.Fatalf("child attribute namespace changed: %+v", title)
	}
}
