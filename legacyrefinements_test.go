package epub

import (
	"bytes"
	"strings"
	"testing"
)

func TestEPUB2RefinementFallback(t *testing.T) {
	path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", "2.0"))
	update := &Information{
		Title:    []MetadataValue{{Value: "Title", Refinements: []Meta{{Property: "custom:title", Value: "T"}}}},
		SubTitle: []MetadataValue{{Value: "Subtitle", Refinements: []Meta{{Property: "custom:subtitle", Value: "S"}}}},
		Creator: []Author{{FullName: "Author", Refinements: []Meta{{
			ID: "author-property", Property: "custom:author", Value: `Q42 & "quoted"`,
			Refines: "#ignored", Lang: "en", Scheme: "custom:entity", Dir: "ltr",
		}}}},
		Contributor: []Author{{FullName: "Contributor", Refinements: []Meta{{Property: "custom:contributor", Value: "C"}}}},
		Identifier:  []Identifier{{Value: "ID", Refinements: []Meta{{Property: "custom:identifier", Value: "I"}}}},
		Date:        []Date{{Stamp: "2026", Refinements: []Meta{{Property: "custom:date", Value: "D"}}}},
		Meta:        []GenericMetadata{{Name: "vendor:entry", Content: "Legacy", Refinements: []Meta{{Property: "custom:legacy", Value: "L"}}}},
		Series:      MetadataValue{Value: "Series", Refinements: []Meta{{Property: "custom:series", Value: "S"}}},
		SeriesIndex: MetadataValue{Value: "2", Refinements: []Meta{{Property: "custom:index", Value: "N"}}},
	}
	for iteration := 0; iteration < 2; iteration++ {
		if err := WriteMetadataToFile(path, update); err != nil {
			t.Fatal(err)
		}
		p, err := GetPackageFromFile(path)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, value := range p.Metadata.Title {
			ids[value.ID] = true
		}
		ids[p.Metadata.Creator[0].ID] = true
		ids[p.Metadata.Contributor[0].ID] = true
		ids[p.Metadata.Identifier[0].ID] = true
		ids[p.Metadata.Date[0].ID] = true
		for _, entry := range p.Metadata.Meta {
			if entry.Meta != nil {
				ids[entry.ID] = true
			}
		}
		count := 0
		for _, entry := range p.Metadata.Meta {
			if strings.HasPrefix(entry.Property, "custom:") {
				t.Fatalf("EPUB 2 contains a newly written native refinement: %+v", entry)
			}
			if entry.Name != legacyRefinementName {
				continue
			}
			m, err := decodeLegacyRefinement(entry.Content)
			if err != nil {
				t.Fatal(err)
			}
			if !ids[strings.TrimPrefix(m.Refines, "#")] {
				t.Fatalf("fallback has a dangling target: %+v", m)
			}
			count++
		}
		if count != 10 { // Nine supplied refinements plus subtitle classification.
			t.Fatalf("expected ten fallback entries, got %d", count)
		}
		got := readWriteTestMetadata(t, path)
		if len(got.SubTitle) != 1 || got.SubTitle[0].Value != "Subtitle" ||
			len(got.Creator[0].Refinements) != 1 {
			t.Fatalf("fallback values not read: %+v", got)
		}
		m := got.Creator[0].Refinements[0]
		if m.Value != `Q42 & "quoted"` || m.ID != "author-property" || m.Lang != "en" || m.Scheme != "custom:entity" || m.Dir != "ltr" {
			t.Fatalf("fallback attributes lost: %+v", m)
		}
		for _, entry := range got.Meta {
			if entry.Name == legacyRefinementName {
				t.Fatal("fallback entries leaked into simplified legacy metadata")
			}
		}
		update = got
	}
	if err := WriteMetadataToFile(path, &Information{Meta: []GenericMetadata{}}); err != nil {
		t.Fatal(err)
	}
	got := readWriteTestMetadata(t, path)
	if len(got.Creator[0].Refinements) != 1 || len(got.Title[0].Refinements) != 1 {
		t.Fatal("clearing legacy metadata removed unrelated fallback entries")
	}
	if err := WriteMetadataToFile(path, &Information{Creator: []Author{{FullName: "Replacement"}}}, "Series"); err != nil {
		t.Fatal(err)
	}
	got = readWriteTestMetadata(t, path)
	if len(got.Creator[0].Refinements) != 0 || got.Series.Value != "" || got.SeriesIndex.Value != "" {
		t.Fatal("replacing fields left stale fallback entries")
	}
}

func TestInvalidEPUB2Fallback(t *testing.T) {
	for _, content := range []string{`{`, `null`, `{}`, `{"Property":"custom:id","Refines":"external"}`} {
		t.Run(content, func(t *testing.T) {
			entry := MetaLegacy{Name: legacyRefinementName, Content: content}
			// Escape JSON for its XML attribute using the existing encoder.
			editor := metadataEditor{}
			editor.addElement(opfNamespace, "meta", "", attr("name", entry.Name), attr("content", entry.Content))
			data, err := encodeMetadata(editor.elements, true)
			if err != nil {
				t.Fatal(err)
			}
			pkg := strings.ReplaceAll(writeTestPackage, "VERSION", "2.0")
			pkg = strings.Replace(pkg, "</metadata>", string(data)+"</metadata>", 1)
			path := createWriteTestEPUB(t, pkg)
			before := readWriteTestFile(t, path)
			if _, err := GetMetadataFromFile(path); err == nil {
				t.Fatal("expected malformed fallback read error")
			}
			if err := WriteMetadataToFile(path, &Information{}); err == nil {
				t.Fatal("expected malformed fallback write error")
			}
			if !bytes.Equal(before, readWriteTestFile(t, path)) {
				t.Fatal("malformed fallback update modified the original")
			}
		})
	}
	path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", "2.0"))
	before := readWriteTestFile(t, path)
	for _, update := range []*Information{
		{Title: []MetadataValue{{Value: "Title", Refinements: []Meta{{Property: "custom:id", Value: "\xff"}}}}},
		{Meta: []GenericMetadata{{Name: legacyRefinementName, Content: "{}"}}},
		{Creator: []Author{{FullName: "Author", Refinements: []Meta{{ID: "primary", Property: "custom:id", Value: "Q42"}}}}},
	} {
		if err := WriteMetadataToFile(path, update); err == nil {
			t.Fatal("expected invalid fallback update error")
		}
		if !bytes.Equal(before, readWriteTestFile(t, path)) {
			t.Fatal("invalid fallback update modified the original")
		}
	}
}
