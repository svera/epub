package epub

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
)

func TestMetadataAttributes(t *testing.T) {
	for _, version := range []string{"2.0", "3.0"} {
		t.Run(version, func(t *testing.T) {
			path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", version))
			attributes := []xml.Attr{
				{Name: xml.Name{Local: "wd_id"}, Value: "Q6197"},
				{Name: xml.Name{Space: "urn:custom", Local: "catalogue"}, Value: `A & "B"`},
			}
			update := &Information{
				Creator:     []Author{{FullName: "Horacio", Role: "aut", FileAs: "Horacio", Attributes: attributes}},
				Contributor: []Author{{FullName: "Translator", Attributes: attributes}},
				Title:       []MetadataValue{{Value: NewMetadataValue("Title").Value, Attributes: attributes}},
				Identifier:  []Identifier{{Value: "ID", Attributes: attributes}},
				Date:        []Date{{Stamp: "2026", Attributes: attributes}},
				Meta:        []GenericMetadata{{Name: "vendor:id", Content: "ID", Attributes: attributes}},
				Series:      MetadataValue{Value: seriesText("Series"), Attributes: attributes},
				SeriesIndex: MetadataValue{Value: seriesText("1"), Attributes: attributes},
			}
			for iteration := 0; iteration < 2; iteration++ {
				if err := WriteMetadataToFile(path, update); err != nil {
					t.Fatal(err)
				}
				got := readWriteTestMetadata(t, path)
				for _, actual := range [][]xml.Attr{
					got.Creator[0].Attributes, got.Contributor[0].Attributes,
					got.Title[0].Attributes, got.Identifier[0].Attributes,
					got.Date[0].Attributes, got.Meta[0].Attributes,
					got.Series.Attributes, got.SeriesIndex.Attributes,
				} {
					if len(actual) != len(attributes) {
						t.Fatalf("attributes lost: %+v", actual)
					}
					for i := range attributes {
						if actual[i] != attributes[i] {
							t.Fatalf("attribute mismatch: %+v", actual)
						}
					}
				}
				e, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				r, err := e.Open(e.rootfile)
				if err != nil {
					t.Fatal(err)
				}
				var content bytes.Buffer
				if _, err := content.ReadFrom(r); err != nil {
					t.Fatal(err)
				}
				r.Close()
				e.Close()
				if !bytes.Contains(content.Bytes(), []byte(`wd_id="Q6197"`)) ||
					bytes.Contains(content.Bytes(), []byte("epub:refinement")) {
					t.Fatal("custom attributes were not written directly")
				}
				update = got
			}
			if err := WriteMetadataToFile(path, &Information{Creator: []Author{{FullName: "No attributes"}}}); err != nil {
				t.Fatal(err)
			}
			got := readWriteTestMetadata(t, path)
			if len(got.Creator[0].Attributes) != 0 || len(got.Title[0].Attributes) == 0 {
				t.Fatal("attribute replacement changed omitted fields")
			}
			if err := WriteMetadataToFile(path, &Information{Series: MetadataValue{Attributes: []xml.Attr{}}}); err != nil {
				t.Fatal(err)
			}
			got = readWriteTestMetadata(t, path)
			if got.Series.String() != "Series" || len(got.Series.Attributes) != 0 || len(got.SeriesIndex.Attributes) == 0 {
				t.Fatal("clearing series attributes changed omitted values or index attributes")
			}
		})
	}
}

func TestReadSigilAttribute(t *testing.T) {
	pkg := strings.ReplaceAll(writeTestPackage, "VERSION", "2.0")
	pkg = strings.Replace(pkg, `<dc:creator id=`, `<dc:creator wd_id="Q6197" id=`, 1)
	path := createWriteTestEPUB(t, pkg)
	got := readWriteTestMetadata(t, path)
	if len(got.Creator[0].Attributes) != 1 || got.Creator[0].Attributes[0].Value != "Q6197" {
		t.Fatalf("Sigil attribute not read: %+v", got.Creator[0])
	}
	if err := WriteMetadataToFile(path, &Information{Creator: got.Creator}); err != nil {
		t.Fatal(err)
	}
	if readWriteTestMetadata(t, path).Creator[0].Attributes[0].Value != "Q6197" {
		t.Fatal("Sigil attribute lost on update")
	}
}

func TestInvalidAttributesPreserveOriginal(t *testing.T) {
	path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", "2.0"))
	before := readWriteTestFile(t, path)
	for _, attributes := range [][]xml.Attr{
		{{Name: xml.Name{Local: "id"}, Value: "duplicate"}},
		{{Name: xml.Name{Local: "bad name"}, Value: "value"}},
		{{Name: xml.Name{Local: "xmlns"}, Value: "urn:override"}},
		{{Name: xml.Name{Local: "wd_id"}, Value: "\x00"}},
		{{Name: xml.Name{Space: "urn:\x00", Local: "wd_id"}, Value: "Q42"}},
		{{Name: xml.Name{Local: "wd_id"}}, {Name: xml.Name{Local: "wd_id"}}},
	} {
		if err := WriteMetadataToFile(path, &Information{Creator: []Author{{FullName: "Author", Attributes: attributes}}}); err == nil {
			t.Fatalf("accepted invalid attributes: %+v", attributes)
		}

		if !bytes.Equal(before, readWriteTestFile(t, path)) {
			t.Fatal("invalid attributes modified the original")
		}
	}
	if err := WriteMetadataToFile(path, &Information{Creator: []Author{{FullName: "Author", Refinements: []Meta{{Property: "cdr:wd_id", Value: "Q42"}}}}}); err == nil {
		t.Fatal("EPUB 2 refinement should require explicit attributes instead")
	}
}

func TestOldRefinementEntriesAreOrdinaryMetadata(t *testing.T) {
	pkg := strings.ReplaceAll(writeTestPackage, "VERSION", "2.0")
	pkg = strings.Replace(pkg, "</metadata>", `<meta name="epub:refinement" content="not JSON"/></metadata>`, 1)
	path := createWriteTestEPUB(t, pkg)
	got := readWriteTestMetadata(t, path)
	found := false
	filtered := make([]GenericMetadata, 0, len(got.Meta))
	for _, m := range got.Meta {
		if m.Name == "epub:refinement" {
			found = true
			if m.Content != "not JSON" {
				t.Fatal("legacy refinement entry was interpreted")
			}
		} else {
			filtered = append(filtered, m)
		}
	}
	if !found {
		t.Fatal("legacy refinement entry was hidden")
	}
	if err := WriteMetadataToFile(path, &Information{Meta: filtered}); err != nil {
		t.Fatal(err)
	}
	for _, m := range readWriteTestMetadata(t, path).Meta {
		if m.Name == "epub:refinement" {
			t.Fatal("legacy refinement entry was not removed")
		}
	}
}

func TestRefinementAttributes(t *testing.T) {
	path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", "3.0"))
	refinement := Meta{
		Property: "alternate-script", Value: "Other script",
		Attributes: []xml.Attr{{Name: xml.Name{Local: "vendor"}, Value: "custom"}},
	}
	update := &Information{Title: []MetadataValue{{Value: NewMetadataValue("Title").Value, Refinements: []Meta{refinement}}}}
	for i := 0; i < 2; i++ {
		if err := WriteMetadataToFile(path, update); err != nil {
			t.Fatal(err)
		}
		update = readWriteTestMetadata(t, path)
		got := update.Title[0].Refinements[0].Attributes
		if len(got) != 1 || got[0] != refinement.Attributes[0] {
			t.Fatalf("refinement attributes lost: %+v", got)
		}
	}
}
