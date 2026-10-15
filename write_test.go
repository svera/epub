package epub

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const writeTestPackage = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf" xmlns:custom="urn:custom" version="VERSION" unique-identifier="primary">
<metadata custom:attribute="keep">
 <dc:identifier id="other">other-id</dc:identifier>
 <dc:identifier id="primary" opf:scheme="UUID">original-id</dc:identifier>
 <dc:title id="title" xml:lang="en">Original title</dc:title>
 <dc:title id="subtitle">Original subtitle</dc:title>
 <meta property="title-type" refines="#subtitle">subtitle</meta>
 <meta id="title-sort" property="file-as" refines="#title">Original</meta>
 <meta property="alternate-script" refines="#title-sort">Old sort</meta>
 <dc:language>en</dc:language>
 <dc:creator id="creator" opf:role="aut">Original author</dc:creator>
 <dc:subject>Old subject</dc:subject>
 <meta name="calibre:series" content="Old series"/>
 <meta name="calibre:series_index" content="1"/>
 <meta name="vendor:legacy" content="keep"/>
 <meta property="dcterms:modified">2020-01-01T00:00:00Z</meta>
 <link href="metadata.xml" rel="record"/>
 <custom:extension custom:attr="value">Keep &amp; preserve</custom:extension>
</metadata>
<manifest custom:attr="keep"><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest>
<spine><itemref idref="chapter"/></spine>
<guide><reference type="text" title="Start" href="chapter.xhtml"/></guide>
</package>`

func TestBuildMetadataRejectsInvalidElements(t *testing.T) {
	for _, element := range []metadataElement{
		{name: xml.Name{Space: opfNamespace, Local: "meta"}, value: "\x00"},
		{name: xml.Name{Space: dcNamespace, Local: "title"}, attributes: []xml.Attr{attr("bad name", "value")}},
		{name: xml.Name{Space: dcNamespace, Local: "title"}, attributes: []xml.Attr{attr("id", "one"), attr("id", "two")}},
	} {
		namespaces, err := newXMLNamespaces(nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := buildMetadata([]metadataElement{element}, namespaces); err == nil {
			t.Fatalf("accepted invalid metadata element: %+v", element)
		}
	}
}

func TestInformationUpdateJSON(t *testing.T) {
	for _, input := range []string{
		`{}`,
		`{"Title":["New title"],"Series":"Series","SeriesIndex":"2"}`,
		`{"Series":null,"SeriesIndex":null}`,
		`{"Series":"","SeriesIndex":""}`,
		`{"Series":"Collection","SeriesIndex":"2"}`,
		`{"Series":{"Attributes":[]}}`,
		`{"Series":{"Refinements":[]},"SeriesIndex":{"Value":null,"Attributes":[]}}`,
		`{"Series":{"Value":"","Attributes":[]},"SeriesIndex":{"Value":"","Refinements":[]}}`,
	} {
		t.Run(input, func(t *testing.T) {
			var update Information
			if err := json.Unmarshal([]byte(input), &update); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(update)
			if err != nil {
				t.Fatal(err)
			}
			var decoded Information
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(update, decoded) {
				t.Fatalf("metadata state lost: %s -> %s", input, encoded)
			}
			if input == `{}` && (update.Title != nil || !update.Series.omitted() || !update.SeriesIndex.omitted()) {
				t.Fatalf("omitted fields are not nil: %+v", update)
			}
			if input == `{"Series":"","SeriesIndex":""}` && (update.Series.Value == nil || update.SeriesIndex.Value == nil) {
				t.Fatal("explicit empty strings must retain non-nil value pointers")
			}
		})
	}
	var update Information
	if err := json.Unmarshal([]byte(`{"Subject":[]}`), &update); err != nil {
		t.Fatal(err)
	}
	if update.Subject == nil || len(update.Subject) != 0 {
		t.Fatal("empty JSON array did not preserve an explicit clear")
	}
	for _, input := range []string{
		`{"Series":[]}`, `{"Series":1}`, `{"Series":false}`,
		`{"Series":{"Unknown":""}}`, `{"SeriesIndex":{"Value":5}}`,
	} {
		var update Information
		if err := json.Unmarshal([]byte(input), &update); err == nil {
			t.Fatalf("invalid optional series accepted: %s", input)
		}
	}
}

func TestWriteSeriesJSONUpdates(t *testing.T) {
	for _, version := range []string{"2.0", "3.0"} {
		for _, tc := range []struct {
			json   string
			series string
			index  string
		}{
			{`{}`, "Old series", "1"},
			{`{"Series":null,"SeriesIndex":null}`, "Old series", "1"},
			{`{"Series":{},"SeriesIndex":{}}`, "Old series", "1"},
			{`{"Series":{"Attributes":[]},"SeriesIndex":{"Refinements":[]}}`, "Old series", "1"},
			{`{"Series":{"Value":null,"Attributes":[]}}`, "Old series", "1"},
			{`{"Series":"New series","SeriesIndex":"2"}`, "New series", "2"},
			{`{"SeriesIndex":""}`, "Old series", ""},
			{`{"Series":""}`, "", ""},
			{`{"Series":{"Value":"","Attributes":[]}}`, "", ""},
			{`{"SeriesIndex":{"Value":"","Attributes":[]}}`, "Old series", ""},
			{`{"Series":"","SeriesIndex":"2"}`, "", ""},
		} {
			t.Run(version+"/"+tc.json, func(t *testing.T) {
				path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", version))
				var update Information
				if err := json.Unmarshal([]byte(tc.json), &update); err != nil {
					t.Fatal(err)
				}
				if err := WriteMetadataToFile(path, &update); err != nil {
					t.Fatal(err)
				}
				info := readWriteTestMetadata(t, path)
				if info.Series.String() != tc.series || info.SeriesIndex.String() != tc.index {
					t.Fatalf("got series %q, index %q; want %q, %q",
						info.Series, info.SeriesIndex, tc.series, tc.index)
				}
				if tc.series == "" && info.Series.Value != nil {
					t.Fatal("absent series must be read with a nil value")
				}
				if tc.index == "" && info.SeriesIndex.Value != nil {
					t.Fatal("absent index must be read with a nil value")
				}
			})
		}
	}
}

func TestWriteSeriesPropertiesWithoutValue(t *testing.T) {
	for _, version := range []string{"2.0", "3.0"} {
		t.Run(version, func(t *testing.T) {
			path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", version))
			attributes := []xml.Attr{{Name: xml.Name{Local: "custom"}, Value: "keep"}}
			update := &Information{
				Series:      MetadataValue{Attributes: attributes},
				SeriesIndex: MetadataValue{Attributes: attributes},
			}
			if version == "3.0" {
				update.Series.Refinements = []Meta{{Property: "custom:series", Value: "name-id"}}
				update.SeriesIndex.Refinements = []Meta{{Property: "custom:index", Value: "index-id"}}
			}
			if err := WriteMetadataToFile(path, update); err != nil {
				t.Fatal(err)
			}
			got := readWriteTestMetadata(t, path)
			if got.Series.String() != "Old series" || got.SeriesIndex.String() != "1" {
				t.Fatal("property-only updates changed series text")
			}
			if !reflect.DeepEqual(got.Series.Attributes, attributes) || !reflect.DeepEqual(got.SeriesIndex.Attributes, attributes) {
				t.Fatal("property-only updates lost attributes")
			}
			if version == "3.0" && (len(got.Series.Refinements) != 1 || len(got.SeriesIndex.Refinements) != 1) {
				t.Fatal("property-only updates lost refinements")
			}
		})
	}
}

func TestMetadataValueJSON(t *testing.T) {
	for _, input := range []string{
		`null`,
		`{}`,
		`{"Value":null}`,
		`""`,
		`"Plain"`,
		`{"Attributes":[]}`,
		`{"Value":null,"Refinements":[]}`,
		`{"Value":"Refined","Refinements":[{"Property":"custom:any_name","Value":"Q42"}]}`,
		`{"Value":"","Refinements":[]}`,
		`{"Value":"Attributed","Attributes":[{"Name":{"Local":"wd_id"},"Value":"Q42"}]}`,
		`{"Value":"","Refinements":null,"Attributes":[]}`,
	} {
		var value MetadataValue
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var got MetadataValue
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, value) {
			t.Fatalf("value did not round trip: %s", data)
		}
	}
	for _, input := range []string{`42`, `[]`, `{"Unknown":"value"}`, `{"Value":[]}`, `{"Refinements":"invalid"}`} {
		var value MetadataValue
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("accepted invalid metadata value: %s", input)
		}
	}
	value := MetadataValue{Value: NewMetadataValue("old").Value, Refinements: []Meta{{Property: "custom:old"}}}
	if err := json.Unmarshal([]byte(`"new"`), &value); err != nil {
		t.Fatal(err)
	}
	if value.String() != "new" || value.Refinements != nil {
		t.Fatal("decoding a string retained stale refinements")
	}
}

func TestWriteOptionalTextFields(t *testing.T) {
	for _, version := range []string{"2.0", "3.0"} {
		for _, field := range []string{
			"Title", "SubTitle", "Language", "Coverage", "Description", "Format",
			"Publisher", "Relation", "Rights", "Source", "Subject", "Type",
		} {
			t.Run(version+"/"+field, func(t *testing.T) {
				path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", version))
				attributes := []xml.Attr{{Name: xml.Name{Local: "custom"}, Value: "keep"}}
				values := []MetadataValue{NewMetadataValue("First"), NewMetadataValue("Second")}
				for i := range values {
					values[i].Attributes = attributes
				}
				if version == "3.0" {
					values[0].Refinements = []Meta{{Property: "custom:keep", Value: "old"}}
				}
				update := &Information{}
				reflect.ValueOf(update).Elem().FieldByName(field).Set(reflect.ValueOf(values))
				if err := WriteMetadataToFile(path, update); err != nil {
					t.Fatal(err)
				}
				original := reflect.ValueOf(readWriteTestMetadata(t, path)).Elem().FieldByName(field).Interface().([]MetadataValue)
				writeJSON := func(entries string) []MetadataValue {
					t.Helper()
					var update Information
					if err := json.Unmarshal([]byte(fmt.Sprintf(`{%q:%s}`, field, entries)), &update); err != nil {
						t.Fatal(err)
					}
					if err := WriteMetadataToFile(path, &update); err != nil {
						t.Fatal(err)
					}
					info := readWriteTestMetadata(t, path)
					return reflect.ValueOf(info).Elem().FieldByName(field).Interface().([]MetadataValue)
				}
				got := writeJSON(`[{},{"Value":null,"Attributes":[]}]`)
				if len(got) != 2 || got[0].String() != "First" || got[1].String() != "Second" {
					t.Fatalf("omitted values lost existing text: %+v", got)
				}
				if !reflect.DeepEqual(got[0].Attributes, attributes) || len(got[1].Attributes) != 0 {
					t.Fatalf("omitted and empty attributes were not distinguished: %+v", got)
				}
				if !reflect.DeepEqual(got[0].Refinements, original[0].Refinements) {
					t.Fatalf("omitted refinements changed: %+v", got[0].Refinements)
				}
				got = writeJSON(`[{"Refinements":[]},{"Attributes":[{"Name":{"Local":"custom"},"Value":"new"}]}]`)
				if got[0].String() != "First" || got[1].String() != "Second" ||
					len(got[0].Refinements) != 0 || !reflect.DeepEqual(got[0].Attributes, attributes) ||
					len(got[1].Attributes) != 1 || got[1].Attributes[0].Value != "new" {
					t.Fatalf("property-only replacement changed unrelated metadata: %+v", got)
				}
				if version == "3.0" {
					got = writeJSON(`[{"Refinements":[{"Property":"custom:new","Value":"new"}]},null]`)
					if got[0].String() != "First" || got[1].String() != "Second" ||
						len(got[0].Refinements) != 1 || got[0].Refinements[0].Property != "custom:new" ||
						!reflect.DeepEqual(got[0].Attributes, attributes) || len(got[1].Attributes) != 1 {
						t.Fatalf("refinement-only update changed text or attributes: %+v", got)
					}
				}
				got = writeJSON(`["",null]`)
				if len(got) != 2 || got[0].Value == nil || got[0].String() != "" ||
					got[1].String() != "Second" || len(got[0].Refinements) != 0 || len(got[0].Attributes) != 0 {
					t.Fatalf("explicit empty text did not replace the value: %+v", got)
				}
				if got = writeJSON(`[]`); len(got) != 0 {
					t.Fatalf("empty list did not clear the field: %+v", got)
				}
			})
		}
	}
}

func TestWriteOptionalTextPreservesInheritedProperties(t *testing.T) {
	path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", "3.0"))
	for _, input := range []string{
		`{"Title":[null],"SubTitle":[{}]}`,
		`{"Title":[{"Attributes":[]}],"SubTitle":[{"Attributes":[]}]}`,
	} {
		var update Information
		if err := json.Unmarshal([]byte(input), &update); err != nil {
			t.Fatal(err)
		}
		if err := WriteMetadataToFile(path, &update); err != nil {
			t.Fatal(err)
		}
		pkg, err := GetPackageFromFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, preserved := range []Meta{
			{ID: "title-sort", Property: "file-as", Refines: "#title", Value: "Original"},
			{Property: "alternate-script", Refines: "#title-sort", Value: "Old sort"},
			{Property: "title-type", Refines: "#subtitle", Value: "subtitle"},
		} {
			found := false
			for _, m := range pkg.Metadata.Meta {
				found = found || reflect.DeepEqual(m.Meta, &preserved)
			}
			if !found {
				t.Fatalf("property-only update removed existing refinement %+v", preserved)
			}
		}
		if input == `{"Title":[null],"SubTitle":[{}]}` && pkg.Metadata.Title[0].Lang != "en" {
			t.Fatal("omitted attributes removed xml:lang")
		}
	}
}

func TestWriteOptionalTextRequiresExistingValue(t *testing.T) {
	for _, version := range []string{"2.0", "3.0"} {
		for _, input := range []string{
			`{"Coverage":[{}]}`,
			`{"Title":["New title",null]}`,
			`{"Title":["New title"],"Description":[{"Attributes":[]}]}`,
		} {
			t.Run(version+"/"+input, func(t *testing.T) {
				path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", version))
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var update Information
				if err := json.Unmarshal([]byte(input), &update); err != nil {
					t.Fatal(err)
				}
				if err := WriteMetadataToFile(path, &update); err == nil || !strings.Contains(err.Error(), "no existing text") {
					t.Fatalf("expected missing text error, got %v", err)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("failed update changed the source EPUB")
				}
			})
		}
	}
}

func TestWriteTextFieldRefinements(t *testing.T) {
	for _, field := range []string{
		"Title", "SubTitle", "Language", "Coverage", "Description", "Format",
		"Publisher", "Relation", "Rights", "Source", "Subject", "Type",
	} {
		t.Run(field, func(t *testing.T) {
			pkg := strings.ReplaceAll(writeTestPackage, "VERSION", "3.0")
			path := createWriteTestEPUB(t, pkg)
			input := fmt.Sprintf(`{%q:[
				{"Value":"First","Refinements":[{"Property":"custom:first","Value":"one","Refines":"#ignored"}]},
				{"Value":"Second","Refinements":[{"Property":"custom:second","Value":"two"}]}
			]}`, field)
			var update Information
			if err := json.Unmarshal([]byte(input), &update); err != nil {
				t.Fatal(err)
			}
			for iteration := 0; iteration < 2; iteration++ {
				if err := WriteMetadataToFile(path, &update); err != nil {
					t.Fatal(err)
				}
				e, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				p, err := e.Package()
				if err != nil {
					t.Fatal(err)
				}
				// Verify targets against the actual package, not just a JSON round trip.
				r, err := e.Open(e.rootfile)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(r)
				r.Close()
				e.Close()
				if err != nil {
					t.Fatal(err)
				}
				source, err := parsePackageXML(data)
				if err != nil {
					t.Fatal(err)
				}
				targets := make(map[string]xml.Name)
				for _, child := range source.children {
					if child.id != "" {
						targets["#"+child.id] = child.name
					}
				}
				count := 0
				for _, meta := range p.Metadata.Meta {
					if meta.Property != "custom:first" && meta.Property != "custom:second" {
						continue
					}
					wantName := strings.ToLower(field)
					if field == "SubTitle" {
						wantName = "title"
					}
					if targets[meta.Refines] != (xml.Name{Space: dcNamespace, Local: wantName}) {
						t.Fatalf("refinement targeted the wrong element: %+v", meta.Meta)
					}
					count++
				}
				if count != 2 {
					t.Fatalf("expected two refinements, got %d", count)
				}
				encoded, err := json.Marshal(readWriteTestMetadata(t, path))
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(encoded, &fields); err != nil {
					t.Fatal(err)
				}
				var got []MetadataValue
				if err := json.Unmarshal(fields[field], &got); err != nil {
					t.Fatal(err)
				}
				expected := []MetadataValue{
					{Value: NewMetadataValue("First").Value, Refinements: []Meta{{Property: "custom:first", Value: "one"}}},
					{Value: NewMetadataValue("Second").Value, Refinements: []Meta{{Property: "custom:second", Value: "two"}}},
				}
				if iteration == 1 {
					expected[0], expected[1] = expected[1], expected[0]
				}
				if len(got) != 2 {
					t.Fatalf("values did not round trip: %+v", got)
				}
				for i, value := range got {
					if len(value.Refinements) != 1 {
						t.Fatalf("refinements did not round trip: %+v", got)
					}
					if value.String() != expected[i].String() ||
						value.Refinements[0].Property != expected[i].Refinements[0].Property ||
						value.Refinements[0].Value != expected[i].Refinements[0].Value {
						t.Fatalf("refinement attached to wrong value: %+v", got)
					}
				}
				// Reorder values; the refinements must follow the values, not their old IDs.
				if iteration == 0 {
					got[0], got[1] = got[1], got[0]
					encoded, err := json.Marshal(map[string]interface{}{field: got})
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(encoded, &update); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestWriteStructuredFieldRefinements(t *testing.T) {
	refinement := func(value string) []Meta {
		return []Meta{{Property: "custom:arbitrary_property", Value: value, Refines: "#ignored"}}
	}
	pkg := strings.ReplaceAll(writeTestPackage, "VERSION", "3.0")
	path := createWriteTestEPUB(t, pkg)
	update := &Information{
		Identifier:  []Identifier{{Value: "Identifier", Refinements: refinement("identifier")}},
		Creator:     []Author{{FullName: "Creator", Refinements: refinement("creator")}},
		Contributor: []Author{{FullName: "Contributor", Refinements: refinement("contributor")}},
		Date:        []Date{{Stamp: "2026-10-15", Refinements: refinement("date")}},
		Meta:        []GenericMetadata{{Name: "vendor:new", Content: "Legacy", Refinements: refinement("legacy")}},
		Series:      MetadataValue{Value: NewMetadataValue("Series").Value, Refinements: refinement("series")},
		SeriesIndex: MetadataValue{Value: NewMetadataValue("2").Value, Refinements: refinement("index")},
	}
	for iteration := 0; iteration < 2; iteration++ {
		if err := WriteMetadataToFile(path, update); err != nil {
			t.Fatal(err)
		}
		p, err := GetPackageFromFile(path)
		if err != nil {
			t.Fatal(err)
		}
		targets := map[string]string{
			p.Metadata.Identifier[0].ID:  "identifier",
			p.Metadata.Creator[0].ID:     "creator",
			p.Metadata.Contributor[0].ID: "contributor",
			p.Metadata.Date[0].ID:        "date",
		}
		for _, meta := range p.Metadata.Meta {
			switch {
			case meta.Name == "vendor:new":
				targets[meta.ID] = "legacy"
			case meta.Property == "belongs-to-collection":
				targets[meta.ID] = "series"
			case meta.Property == "group-position":
				targets[meta.ID] = "index"
			}
		}
		count := 0
		for _, meta := range p.Metadata.Meta {
			if meta.Property == "custom:arbitrary_property" {
				if targets[strings.TrimPrefix(meta.Refines, "#")] != meta.Value || meta.Refines == "#" {
					t.Fatalf("incorrect refinement target: %+v", meta.Meta)
				}
				count++
			}
		}
		if count != 7 {
			t.Fatalf("expected seven refinements, got %d", count)
		}
		update = readWriteTestMetadata(t, path)
		count = 0
		for _, group := range update.refinementGroups() {
			for _, meta := range group {
				if meta.Property == "custom:arbitrary_property" {
					count++
				}
			}
		}
		if count != 7 {
			t.Fatalf("reader lost field refinements: %+v", update)
		}
	}
	// Updating just the index must retain the collection's custom refinements.
	if err := WriteMetadataToFile(path, &Information{SeriesIndex: NewMetadataValue("3")}); err != nil {
		t.Fatal(err)
	}
	got := readWriteTestMetadata(t, path)
	if len(got.Series.Refinements) != 1 || len(got.SeriesIndex.Refinements) != 0 {
		t.Fatal("index update did not preserve the omitted series or replace index refinements")
	}
	if err := WriteMetadataToFile(path, &Information{Series: MetadataValue{Refinements: []Meta{}}}); err != nil {
		t.Fatal(err)
	}
	got = readWriteTestMetadata(t, path)
	if got.Series.String() != "Series" || len(got.Series.Refinements) != 0 || got.SeriesIndex.String() != "3" {
		t.Fatal("clearing just series refinements changed the series or index values")
	}
}

func createWriteTestEPUB(t *testing.T, pkg string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "book.epub")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	if err := w.SetComment("archive comment"); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		name, content string
		method        uint16
	}{
		{"mimetype", "application/epub+zip", zip.Store},
		{"META-INF/container.xml", `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`, zip.Deflate},
		{"OPS/book.opf", pkg, zip.Deflate},
		{"OPS/chapter.xhtml", "<html>Unchanged chapter</html>", zip.Deflate},
		{"OPS/metadata.xml", "<record/>", zip.Deflate},
	} {
		dst, err := w.CreateHeader(&zip.FileHeader{Name: entry.name, Method: entry.method, Comment: "entry comment"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(dst, entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	return path
}

func readWriteTestFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readWriteTestMetadata(t *testing.T, path string) *Information {
	t.Helper()
	info, err := GetMetadataFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestWriteMetadataUpdates(t *testing.T) {
	cases := []struct {
		name   string
		update Information
	}{
		{
			name: "partial",
			update: Information{
				Title: []MetadataValue{NewMetadataValue(`New <Title> & "more"`)}, Subject: []MetadataValue{},
			},
		},
		{
			name: "all fields",
			update: Information{
				Identifier: []Identifier{{Scheme: "ISBN", Value: "978-test"}},
				Title:      []MetadataValue{NewMetadataValue("Title"), NewMetadataValue("Another title")}, SubTitle: []MetadataValue{NewMetadataValue("Subtitle")},
				Language: []MetadataValue{NewMetadataValue("es"), NewMetadataValue("en")}, Contributor: []Author{{FullName: "Translator", Role: "trl", FileAs: "Translator"}},
				Creator:  []Author{{FullName: "New author", Role: "aut", FileAs: "Author, New"}},
				Date:     []Date{{Stamp: "2026-10-15", Event: "publication"}},
				Coverage: []MetadataValue{NewMetadataValue("Coverage")}, Description: []MetadataValue{NewMetadataValue("Description")},
				Format: []MetadataValue{NewMetadataValue("Format")}, Publisher: []MetadataValue{NewMetadataValue("Publisher")},
				Relation: []MetadataValue{NewMetadataValue("Relation")}, Rights: []MetadataValue{NewMetadataValue("Rights")},
				Source: []MetadataValue{NewMetadataValue("Source")}, Subject: []MetadataValue{NewMetadataValue("Subject")}, Type: []MetadataValue{NewMetadataValue("Type")},
				Meta:   []GenericMetadata{{Name: "vendor:new", Content: "new content"}},
				Series: NewMetadataValue("New series"), SeriesIndex: NewMetadataValue("2.5"),
			},
		},
	}
	for _, version := range []string{"2.0", "3.0"} {
		for _, tc := range cases {
			t.Run(version+"/"+tc.name, func(t *testing.T) {
				original := strings.ReplaceAll(writeTestPackage, "VERSION", version)
				path := createWriteTestEPUB(t, original)
				var want *Information
				if tc.name == "partial" {
					want = readWriteTestMetadata(t, path)
					want.Title, want.Subject = tc.update.Title, []MetadataValue{}
				} else {
					expected := tc.update
					want = &expected
					if version == "2.0" {
						want.Meta = append(append([]GenericMetadata(nil), want.Meta...),
							GenericMetadata{Name: "calibre:series", Content: want.Series.String()},
							GenericMetadata{Name: "calibre:series_index", Content: want.SeriesIndex.String()})
					}
				}
				before, err := zip.OpenReader(path)
				if err != nil {
					t.Fatal(err)
				}
				defer before.Close()
				if err := WriteMetadataToFile(path, &tc.update); err != nil {
					t.Fatal(err)
				}
				info := readWriteTestMetadata(t, path)
				if !reflect.DeepEqual(info, want) {
					t.Fatalf("round trip mismatch:\ngot:  %+v\nwant: %+v", info, want)
				}
				after, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer after.Close()
				if after.Comment != before.Comment || len(after.File) != len(before.File) {
					t.Fatal("archive properties changed")
				}
				for i, old := range before.File {
					f := after.File[i]
					if old.Name != f.Name || old.Method != f.Method || old.Comment != f.Comment {
						t.Fatalf("entry properties changed: %s", old.Name)
					}
					if f.Name == after.rootfile {
						r, err := f.Open()
						if err != nil {
							t.Fatal(err)
						}
						data, err := io.ReadAll(r)
						r.Close()
						if err != nil {
							t.Fatal(err)
						}
						for _, fragment := range []string{
							`<metadata custom:attribute="keep">`,
							`<custom:extension custom:attr="value">Keep &amp; preserve</custom:extension>`,
							`<link href="metadata.xml" rel="record"/>`,
							original[strings.Index(original, "<manifest"):],
						} {
							if !bytes.Contains(data, []byte(fragment)) {
								t.Errorf("lost original XML: %s", fragment)
							}
						}
						if bytes.Contains(data, []byte("Old sort")) || bytes.Contains(data, []byte(`id="title-sort"`)) {
							t.Error("stale refinements retained")
						}
						continue
					}
					oldRaw, err := old.OpenRaw()
					if err != nil {
						t.Fatal(err)
					}
					newRaw, err := f.OpenRaw()
					if err != nil {
						t.Fatal(err)
					}
					oldData, err := io.ReadAll(oldRaw)
					if err != nil {
						t.Fatal(err)
					}
					newData, err := io.ReadAll(newRaw)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(oldData, newData) || !reflect.DeepEqual(old.FileHeader, f.FileHeader) {
						t.Errorf("untouched archive entry changed: %s", f.Name)
					}
				}
				stat, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if stat.Mode().Perm() != 0640 {
					t.Errorf("permissions changed: %v", stat.Mode())
				}
				if err := WriteMetadataToFile(path, &tc.update); err != nil {
					t.Fatalf("repeated update: %v", err)
				}
				info = readWriteTestMetadata(t, path)
				if !reflect.DeepEqual(info, want) {
					t.Fatalf("repeated update changed metadata:\ngot: %+v\nwant: %+v", info, want)
				}
				if err := WriteMetadataToFile(path, &Information{
					SubTitle: []MetadataValue{}, Meta: []GenericMetadata{},
				}, "Series"); err != nil {
					t.Fatal(err)
				}
				info = readWriteTestMetadata(t, path)
				if info.Series.String() != "" || info.SeriesIndex.String() != "" || len(info.SubTitle) != 0 || len(info.Meta) != 0 {
					t.Fatalf("clear failed: %+v", info)
				}
			})
		}
	}
}

func TestWriteMetadataFailuresPreserveOriginal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pkg    string
		update *Information
		clear  []string
	}{
		{"nil update", writeTestPackage, nil, nil},
		{"remove primary identifier", writeTestPackage, &Information{Identifier: []Identifier{}}, nil},
		{"malformed XML", "<package>", &Information{Title: []MetadataValue{NewMetadataValue("Title")}}, nil},
		{"missing metadata", `<package xmlns="http://www.idpf.org/2007/opf"/>`, &Information{Title: []MetadataValue{NewMetadataValue("Title")}}, nil},
		{"invalid XML character", writeTestPackage, &Information{Title: []MetadataValue{NewMetadataValue("bad\x00title")}}, nil},
		{"invalid UTF-8", writeTestPackage, &Information{Title: []MetadataValue{NewMetadataValue("bad\xfftitle")}}, nil},
		{"invalid attribute", writeTestPackage, &Information{Creator: []Author{{FullName: "Author", FileAs: "\x00"}}}, nil},
		{"unknown clear field", writeTestPackage, &Information{}, []string{"Unknown"}},
		{"empty refinement property", writeTestPackage, &Information{Creator: []Author{{FullName: "Author", Refinements: []Meta{{Value: "Q42"}}}}}, nil},
		{"duplicate refinement ID", writeTestPackage, &Information{Creator: []Author{{FullName: "Author", Refinements: []Meta{{ID: "primary", Property: "custom:author", Value: "Q42"}}}}}, nil},
		{"invalid refinement text", writeTestPackage, &Information{Creator: []Author{{FullName: "Author", Refinements: []Meta{{Property: "custom:author", Value: "\x00"}}}}}, nil},
		{"empty text refinement property", writeTestPackage, &Information{Title: []MetadataValue{{Value: NewMetadataValue("Title").Value, Refinements: []Meta{{Property: " "}}}}}, nil},
		{"controlled legacy series refinement", writeTestPackage, &Information{Meta: []GenericMetadata{{Name: "calibre:series", Content: "Series", Refinements: []Meta{{Property: "custom:id", Value: "id"}}}}}, nil},
		{"refinement-only series without a name", strings.ReplaceAll(strings.ReplaceAll(writeTestPackage, "calibre:series", "vendor:unused"), "Old series", ""), &Information{Series: MetadataValue{Refinements: []Meta{{Property: "custom:series", Value: "id"}}}}, nil},
		{"index without a series", strings.ReplaceAll(writeTestPackage, "calibre:series", "vendor:unused"), &Information{SeriesIndex: NewMetadataValue("2")}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := createWriteTestEPUB(t, strings.ReplaceAll(tc.pkg, "VERSION", "3.0"))
			before := readWriteTestFile(t, path)
			if err := WriteMetadataToFile(path, tc.update, tc.clear...); err == nil {
				t.Fatal("expected an error")
			}
			if !bytes.Equal(before, readWriteTestFile(t, path)) {
				t.Fatal("failed update modified original")
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("temporary files not cleaned up: %v", entries)
			}
		})
	}
}

func TestWriteStandardRefinementProperties(t *testing.T) {
	path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", "3.0"))
	if err := WriteMetadataToFile(path, &Information{
		Creator: []Author{{
			FullName: "Author", Role: "aut", FileAs: "Old file-as",
			Refinements: []Meta{{Property: "role", Value: "edt"}, {Property: "file-as", Value: "New file-as"}},
		}},
		Title: []MetadataValue{{Value: NewMetadataValue("Title").Value, Refinements: []Meta{{Property: "title-type", Value: "short"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	got := readWriteTestMetadata(t, path)
	if got.Creator[0].Role != "edt" || got.Creator[0].FileAs != "New file-as" {
		t.Fatalf("explicit refinements did not override convenience values: %+v", got.Creator)
	}
	if len(got.Title[0].Refinements) != 1 || got.Title[0].Refinements[0].Property != "title-type" {
		t.Fatal("standard property was not accepted as a refinement")
	}
}

func TestWriteAuthorRefinements(t *testing.T) {
	pkg := strings.ReplaceAll(writeTestPackage, "VERSION", "3.0")
	pkg = strings.Replace(pkg, `version="3.0"`, `version="3.0" prefix="cdr: https://example.org/cdr/"`, 1)
	pkg = strings.Replace(pkg, `<dc:subject>`, `<meta property="cdr:wd_id" refines="#creator" id="old-author-id">Q1</meta><dc:subject>`, 1)
	path := createWriteTestEPUB(t, pkg)
	info := readWriteTestMetadata(t, path)
	if len(info.Creator[0].Refinements) != 1 || info.Creator[0].Refinements[0].Value != "Q1" {
		t.Fatalf("existing custom refinement not read: %+v", info.Creator)
	}
	if err := WriteMetadataToFile(path, &Information{Publisher: []MetadataValue{NewMetadataValue("Publisher")}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(readWriteTestMetadata(t, path).Creator, info.Creator) {
		t.Fatal("omitted creators lost refinements")
	}
	update := &Information{
		Creator: []Author{
			{FullName: "Updated author", Role: "aut", FileAs: "Author, Updated", Refinements: []Meta{{
				ID: "old-author-id", Property: "cdr:wd_id", Value: "Q42",
				Refines: "#ignored", Scheme: "cdr:entity", Lang: "en", Dir: "ltr",
			}}},
			{FullName: "New author", Refinements: []Meta{{
				ID: "epub-metadata-2", Property: "cdr:wd_id", Value: "Q43",
			}}},
		},
		Contributor: []Author{{FullName: "Contributor", Refinements: []Meta{{
			Property: "cdr:wd_id", Value: "Q44",
		}}}},
	}
	for i := 0; i < 2; i++ {
		if err := WriteMetadataToFile(path, update); err != nil {
			t.Fatal(err)
		}
		p, err := GetPackageFromFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got := readWriteTestMetadata(t, path)
		targets := []string{p.Metadata.Creator[0].ID, p.Metadata.Creator[1].ID, p.Metadata.Contributor[0].ID}
		authors := append(append([]Author(nil), got.Creator...), got.Contributor...)
		wanted := append(append([]Author(nil), update.Creator...), update.Contributor...)
		for j, author := range authors {
			expected := wanted[j]
			expected.Refinements = append([]Meta(nil), expected.Refinements...)
			expected.Refinements[0].Refines = "#" + targets[j]
			if !reflect.DeepEqual(author, expected) {
				t.Fatalf("author refinement mismatch:\ngot: %+v\nwant: %+v", author, expected)
			}
		}
		if p.Prefix != "cdr: https://example.org/cdr/" || targets[0] != "creator" {
			t.Fatal("package prefix or existing creator ID changed")
		}
		count := 0
		for _, m := range p.Metadata.Meta {
			if m.Property == "cdr:wd_id" {
				count++
			}
		}
		if count != 3 {
			t.Fatalf("expected three custom refinements, got %d", count)
		}
	}
	if err := WriteMetadataToFile(path, &Information{Creator: []Author{{FullName: "No refinements"}}}); err != nil {
		t.Fatal(err)
	}
	got := readWriteTestMetadata(t, path)
	if len(got.Creator[0].Refinements) != 0 || len(got.Contributor[0].Refinements) != 1 {
		t.Fatalf("replacing creator did not clear just its refinements: %+v", got)
	}
}

func TestWriteMetadataSeriesRefinements(t *testing.T) {
	pkg := strings.ReplaceAll(writeTestPackage, "VERSION", "3.0")
	pkg = strings.Replace(pkg, `<meta name="calibre:series" content="Old series"/>`, `
<meta property="belongs-to-collection" id="series">Old series</meta>
<meta property="collection-type" refines="#series">series</meta>
<meta property="group-position" refines="#series">1</meta>
<meta property="belongs-to-collection" id="other-collection">Other collection</meta>
<meta property="collection-type" refines="#other-collection">set</meta>`, 1)
	path := createWriteTestEPUB(t, pkg)
	if err := WriteMetadataToFile(path, &Information{SeriesIndex: NewMetadataValue("4")}); err != nil {
		t.Fatal(err)
	}
	info := readWriteTestMetadata(t, path)
	if info.Series.String() != "Old series" || info.SeriesIndex.String() != "4" {
		t.Fatalf("index-only update changed series: %+v", info)
	}
	p, err := GetPackageFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	otherFound := false
	for _, m := range p.Metadata.Meta {
		if m.ID == "other-collection" && m.Value == "Other collection" {
			otherFound = true
		}
	}
	if !otherFound {
		t.Fatal("unrelated collection was removed")
	}
	if err := WriteMetadataToFile(path, &Information{}, "Series"); err != nil {
		t.Fatal(err)
	}
	info = readWriteTestMetadata(t, path)
	if info.Series.String() != "" || info.SeriesIndex.String() != "" {
		t.Fatalf("unrelated set treated as series after clearing: %+v", info)
	}
}

func TestWriteMetadataClearFields(t *testing.T) {
	cases := []struct {
		name       string
		update     Information
		clear      string
		wantSeries string
		wantIndex  string
	}{
		{name: "omitted values preserve series", wantSeries: "Old series", wantIndex: "1"},
		{
			name: "empty series clears both values", update: Information{Series: NewMetadataValue("")},
		},
		{
			name: "empty index preserves series", update: Information{SeriesIndex: NewMetadataValue("")},
			wantSeries: "Old series",
		},
		{
			name: "clear index overrides update", update: Information{SeriesIndex: NewMetadataValue("9")},
			clear: "SeriesIndex", wantSeries: "Old series",
		},
		{
			name:   "clear series overrides update",
			update: Information{Series: NewMetadataValue("Replacement"), SeriesIndex: NewMetadataValue("9")}, clear: "Series",
		},
	}
	for _, version := range []string{"2.0", "3.0"} {
		for _, tc := range cases {
			t.Run(version+"/"+tc.name, func(t *testing.T) {
				path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", version))
				var clear []string
				if tc.clear != "" {
					clear = []string{tc.clear}
				}
				if err := WriteMetadataToFile(path, &tc.update, clear...); err != nil {
					t.Fatal(err)
				}
				info := readWriteTestMetadata(t, path)
				if info.Series.String() != tc.wantSeries || info.SeriesIndex.String() != tc.wantIndex {
					t.Fatalf("got series %q, index %q; want %q, %q",
						info.Series, info.SeriesIndex, tc.wantSeries, tc.wantIndex)
				}
			})
		}
	}
}

func TestWriteMetadataModifiedAndIDs(t *testing.T) {
	pkg := strings.ReplaceAll(writeTestPackage, "VERSION", "3.0")
	path := createWriteTestEPUB(t, pkg)
	if err := WriteMetadataToFile(path, &Information{Publisher: []MetadataValue{NewMetadataValue("New publisher")}}); err != nil {
		t.Fatal(err)
	}
	p, err := GetPackageFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	modifiedCount := 0
	for _, m := range p.Metadata.Meta {
		if m.Property == "dcterms:modified" {
			modifiedCount++
			stamp, err := time.Parse("2006-01-02T15:04:05Z", m.Value)
			if err != nil || time.Since(stamp) > time.Minute {
				t.Fatalf("invalid modified timestamp: %q", m.Value)
			}
		}
	}
	if modifiedCount != 1 {
		t.Fatalf("expected one modified timestamp, got %d", modifiedCount)
	}
	if err := WriteMetadataToFile(path, &Information{
		Identifier: []Identifier{{Value: "first"}, {Value: "second"}, {Value: "third"}},
	}); err != nil {
		t.Fatal(err)
	}
	p, err = GetPackageFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool)
	for _, id := range p.Metadata.Identifier {
		if ids[id.ID] || id.ID == "" {
			t.Fatalf("duplicate or empty identifier ID: %q", id.ID)
		}
		ids[id.ID] = true
	}
	if !ids[p.UniqueIdentifier] {
		t.Fatal("primary identifier reference broken")
	}
}

func TestWriteMetadataNoopAndPrefixedPackage(t *testing.T) {
	pkg := strings.ReplaceAll(writeTestPackage, "VERSION", "3.0")
	pkg = strings.ReplaceAll(pkg, `<package xmlns=`, `<opf:package xmlns=`)
	pkg = strings.ReplaceAll(pkg, `</package>`, `</opf:package>`)
	pkg = strings.ReplaceAll(pkg, `<metadata `, `<opf:metadata `)
	pkg = strings.ReplaceAll(pkg, `</metadata>`, `</opf:metadata>`)
	path := createWriteTestEPUB(t, pkg)
	if err := WriteMetadataToFile(path, &Information{}); err != nil {
		t.Fatal(err)
	}
	e, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Open(e.rootfile)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	r.Close()
	e.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != pkg {
		t.Fatal("empty update changed package XML")
	}
	if err := WriteMetadataToFile(path, &Information{Title: []MetadataValue{NewMetadataValue("New title")}}); err != nil {
		t.Fatal(err)
	}
	info := readWriteTestMetadata(t, path)
	if !reflect.DeepEqual(info.Title, []MetadataValue{NewMetadataValue("New title")}) {
		t.Fatalf("prefixed package update failed: %+v", info)
	}
}

func TestWriteMetadataRejectsSymlinks(t *testing.T) {
	path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", "3.0"))
	link := filepath.Join(t.TempDir(), "link.epub")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	before := readWriteTestFile(t, path)
	if err := WriteMetadataToFile(link, &Information{Title: []MetadataValue{NewMetadataValue("New")}}); err == nil {
		t.Fatal("expected symlink error")
	}
	if !bytes.Equal(before, readWriteTestFile(t, path)) {
		t.Fatal("symlink target changed")
	}
}

func TestWriteMetadataExistingEPUBs(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(testdataPath, "*.epub"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no EPUB fixtures")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "copy.epub")
			if err := os.WriteFile(path, readWriteTestFile(t, file), 0600); err != nil {
				t.Fatal(err)
			}
			before := readWriteTestMetadata(t, path)
			if err := WriteMetadataToFile(path, &Information{Title: []MetadataValue{NewMetadataValue("Updated title")}}); err != nil {
				t.Fatal(err)
			}
			after := readWriteTestMetadata(t, path)
			before.Title = []MetadataValue{NewMetadataValue("Updated title")}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("unexpected metadata changes:\ngot: %+v\nwant: %+v", after, before)
			}
		})
	}
}
