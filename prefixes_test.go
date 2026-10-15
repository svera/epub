package epub

import (
	"bytes"
	"strings"
	"testing"
)

func TestWritePackagePrefixes(t *testing.T) {
	for _, attribute := range []string{
		"",
		` prefix=""`,
		` prefix='existing: https://example.org/existing/'`,
		` prefix="existing: https://example.org/existing/"`,
	} {
		t.Run(attribute, func(t *testing.T) {
			pkg := strings.ReplaceAll(writeTestPackage, "VERSION", "3.0")
			pkg = strings.Replace(pkg, `version="3.0"`, `version="3.0"`+attribute, 1)
			path := createWriteTestEPUB(t, pkg)
			update := &Information{
				Prefixes: map[string]string{"cdr": "https://example.org/cdr/?a=1&b=2", "other": "urn:example:other"},
				Creator:  []Author{{FullName: "Author", Refinements: []Meta{{Property: "cdr:wd_id", Value: "Q42"}}}},
			}
			for iteration := 0; iteration < 2; iteration++ {
				if err := WriteMetadataToFile(path, update); err != nil {
					t.Fatal(err)
				}
				got := readWriteTestMetadata(t, path)
				if got.Prefixes["cdr"] != update.Prefixes["cdr"] ||
					got.Prefixes["other"] != update.Prefixes["other"] {
					t.Fatalf("prefixes did not round trip: %+v", got.Prefixes)
				}
				if strings.Contains(attribute, "existing:") && got.Prefixes["existing"] != "https://example.org/existing/" {
					t.Fatal("unrelated prefix was lost")
				}
				p, err := GetPackageFromFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Count(p.Prefix, "cdr:") != 1 || len(got.Creator[0].Refinements) != 1 {
					t.Fatal("repeated update duplicated declarations or lost refinement")
				}
			}
			if err := WriteMetadataToFile(path, &Information{Publisher: []MetadataValue{{Value: "Publisher"}}}); err != nil {
				t.Fatal(err)
			}
			if readWriteTestMetadata(t, path).Prefixes["cdr"] != update.Prefixes["cdr"] {
				t.Fatal("omitted prefixes were not preserved")
			}
		})
	}
}

func TestPrefixUpdatePreservesXML(t *testing.T) {
	for _, pkg := range []string{
		`<package xmlns="http://www.idpf.org/2007/opf" version="3.0" custom="prefix='not-an-attribute'"><metadata/></package>`,
		`<opf:package xmlns:opf="http://www.idpf.org/2007/opf" version="3.0" prefix='old: urn:old'><opf:metadata/></opf:package>`,
	} {
		p, err := newPackageDocument(strings.NewReader(pkg))
		if err != nil {
			t.Fatal(err)
		}
		result, err := updatePackagePrefixes([]byte(pkg), p, map[string]string{"cdr": "urn:cdr"})
		if err != nil {
			t.Fatal(err)
		}
		updated, err := newPackageDocument(bytes.NewReader(result))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(updated.Prefix, "cdr: urn:cdr") ||
			!bytes.HasSuffix(result, []byte(pkg[strings.Index(pkg, ">")+1:])) {
			t.Fatalf("prefix update changed package content: %s", result)
		}
		if strings.Contains(pkg, `custom=`) && !bytes.Contains(result, []byte(`custom="prefix='not-an-attribute'"`)) {
			t.Fatal("attribute contents were mistaken for a prefix declaration")
		}
	}
}

func TestInvalidPrefixUpdatesPreserveFile(t *testing.T) {
	for _, tc := range []struct {
		name, version, existing string
		prefixes                map[string]string
	}{
		{"conflict", "3.0", "cdr: urn:old", map[string]string{"cdr": "urn:new"}},
		{"invalid prefix", "3.0", "", map[string]string{"bad:name": "urn:new"}},
		{"relative IRI", "3.0", "", map[string]string{"cdr": "/relative"}},
		{"whitespace IRI", "3.0", "", map[string]string{"cdr": "urn:with space"}},
		{"malformed existing", "3.0", "broken", map[string]string{"cdr": "urn:new"}},
		{"EPUB 2", "2.0", "", map[string]string{"cdr": "urn:new"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkg := strings.ReplaceAll(writeTestPackage, "VERSION", tc.version)
			pkg = strings.Replace(pkg, `unique-identifier=`, `prefix="`+tc.existing+`" unique-identifier=`, 1)
			path := createWriteTestEPUB(t, pkg)
			before := readWriteTestFile(t, path)
			if err := WriteMetadataToFile(path, &Information{Prefixes: tc.prefixes}); err == nil {
				t.Fatal("expected prefix update error")
			}
			if !bytes.Equal(before, readWriteTestFile(t, path)) {
				t.Fatal("invalid update modified original file")
			}
		})
	}
}
