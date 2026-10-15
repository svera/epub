package epub

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
)

func TestPreferredMetadataNamespaces(t *testing.T) {
	const vocab = "https://github.com/svera/coreander/vocab/"
	for _, version := range []string{"2.0", "3.0"} {
		t.Run(version, func(t *testing.T) {
			pkg := strings.ReplaceAll(writeTestPackage, "VERSION", version)
			pkg = strings.Replace(pkg, `<metadata custom:attribute="keep">`,
				`<metadata custom:attribute="keep" xmlns:dc="`+dcNamespace+`" xmlns:opf="`+opfNamespace+`">`, 1)
			path := createWriteTestEPUB(t, pkg)
			update := &Information{
				Namespaces: map[string]string{"cdr": vocab},
				Creator: []Author{{
					FullName: "Isaac Asimov", Role: "aut", FileAs: "Asimov, Isaac",
					Attributes: []xml.Attr{{Name: xml.Name{Space: vocab, Local: "wd_id"}, Value: "Q34981"}},
				}},
			}
			for i := 0; i < 2; i++ {
				if err := WriteMetadataToFile(path, update); err != nil {
					t.Fatal(err)
				}
				e, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				r, err := e.Open(e.rootfile)
				if err != nil {
					e.Close()
					t.Fatal(err)
				}
				var content bytes.Buffer
				if _, err := content.ReadFrom(r); err != nil {
					t.Fatal(err)
				}
				r.Close()
				e.Close()
				output := content.String()
				creatorStart := strings.Index(output, "<dc:creator")
				if creatorStart < 0 {
					t.Fatalf("creator did not reuse dc prefix: %s", output)
				}
				creator := output[creatorStart : creatorStart+strings.Index(output[creatorStart:], ">")+1]
				if strings.Contains(creator, "xmlns") || !strings.Contains(creator, `cdr:wd_id="Q34981"`) {
					t.Fatalf("unexpected creator namespaces: %s", creator)
				}
				if version == "2.0" && (!strings.Contains(creator, `opf:role="aut"`) ||
					!strings.Contains(creator, `opf:file-as="Asimov, Isaac"`)) {
					t.Fatalf("managed attributes did not reuse opf: %s", creator)
				}
				if strings.Count(output, `xmlns:cdr=`) != 1 {
					t.Fatal("custom namespace was not declared exactly once")
				}
				metadataStart := strings.Index(output, "<metadata")
				metadata := output[metadataStart : metadataStart+strings.Index(output[metadataStart:], ">")+1]
				if !strings.Contains(metadata, `xmlns:cdr="`+vocab+`"`) {
					t.Fatalf("namespace was not declared on metadata: %s", metadata)
				}
				got := readWriteTestMetadata(t, path)
				if got.Creator[0].Attributes[0] != update.Creator[0].Attributes[0] ||
					got.Creator[0].Role != "aut" || got.Creator[0].FileAs != "Asimov, Isaac" {
					t.Fatalf("namespace update changed metadata: %+v", got.Creator[0])
				}
				// Read/write without preferences must reuse the declaration from the first write.
				update = &Information{Creator: got.Creator}
			}
		})
	}
}

func TestNamespaceScopeAndDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, pkg, expected string
	}{
		{
			"inherited prefix",
			`<package xmlns="` + opfNamespace + `" xmlns:d="` + dcNamespace + `" xmlns:v="urn:custom" version="2.0"><metadata/></package>`,
			`<d:creator`,
		},
		{
			"default namespace cannot qualify attributes",
			`<p:package xmlns:p="` + opfNamespace + `" version="2.0"><p:metadata xmlns="urn:custom"/></p:package>`,
			`ns:wd_id="Q42"`,
		},
		{
			"shadowed namespace",
			`<package xmlns="` + opfNamespace + `" xmlns:v="urn:custom" version="2.0"><metadata xmlns:v="urn:other"/></package>`,
			`ns:wd_id="Q42"`,
		},
		{
			"child-only namespace",
			`<package xmlns="` + opfNamespace + `" version="2.0"><metadata><meta xmlns:v="urn:custom" name="other" content="keep"/></metadata></package>`,
			`ns:wd_id="Q42"`,
		},
		{
			"generated prefix collision",
			`<package xmlns="` + opfNamespace + `" xmlns:ns="urn:other" version="2.0"><metadata/></package>`,
			`ns1:wd_id="Q42"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			update := &Information{Creator: []Author{{
				FullName: "Author", Attributes: []xml.Attr{{Name: xml.Name{Space: "urn:custom", Local: "wd_id"}, Value: "Q42"}},
			}}}
			out, err := updatePackageMetadata([]byte(tc.pkg), update, nil)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Contains(out, []byte(tc.expected)) {
				t.Fatalf("missing expected qualified name %q: %s", tc.expected, out)
			}
			p, err := newPackageDocument(bytes.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			if p.Metadata.Creator[0].Attributes[0] != update.Creator[0].Attributes[0] {
				t.Fatalf("attribute namespace was changed: %s", out)
			}
		})
	}
}

func TestNamespaceDeclarationsPreserveXML(t *testing.T) {
	pkg := `<package xmlns="` + opfNamespace + `" xmlns:c="urn:custom" version="2.0"><metadata custom='a > b'><meta name="keep" content="unchanged"/></metadata><manifest/></package>`
	update := &Information{Namespaces: map[string]string{"chosen": "urn:custom", "other": "urn:other?a=1&b=2"}}
	out, err := updatePackageMetadata([]byte(pkg), update, nil)
	if err != nil {
		t.Fatal(err)
	}
	source, err := parsePackageXML(out)
	if err != nil {
		t.Fatal(err)
	}
	if source.metadata.SelectAttrValue("custom", "") != "a > b" ||
		source.metadata.SelectAttrValue("xmlns:chosen", "") != "urn:custom" ||
		source.metadata.SelectAttrValue("xmlns:other", "") != "urn:other?a=1&b=2" ||
		!bytes.Contains(out, []byte(`<meta name="keep" content="unchanged"/>`)) ||
		!bytes.Contains(out, []byte(`<manifest/>`)) {
		t.Fatalf("namespace-only update changed unrelated content:\n%s", out)
	}
	update.Creator = []Author{{
		FullName: "Author", Attributes: []xml.Attr{{Name: xml.Name{Space: "urn:custom", Local: "id"}, Value: "ID"}},
	}}
	out, err = updatePackageMetadata(out, update, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`chosen:id="ID"`)) {
		t.Fatalf("preferred prefix did not override inherited alias: %s", out)
	}
}

func TestNamespaceOnlyUpdates(t *testing.T) {
	for _, version := range []string{"2.0", "3.0"} {
		pkg := `<p:package xmlns:p="` + opfNamespace + `" version="` + version + `"><p:metadata /></p:package>`
		update := &Information{Namespaces: map[string]string{"cdr": "urn:cdr?a=1&b=2"}}
		out, err := updatePackageMetadata([]byte(pkg), update, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(out, []byte(`xmlns:cdr="urn:cdr?a=1&amp;b=2"`)) {
			t.Fatalf("missing escaped declaration: %s", out)
		}
		again, err := updatePackageMetadata(out, update, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out, again) {
			t.Fatalf("identical namespace mapping was not a no-op:\n%s\n%s", out, again)
		}
		if version == "3.0" && !bytes.Contains(out, []byte(`property="dcterms:modified"`)) {
			t.Fatal("namespace-only edit did not refresh modified time")
		}
	}
}

func TestInvalidNamespaceUpdatesPreserveFile(t *testing.T) {
	path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", "2.0"))
	before := readWriteTestFile(t, path)
	for _, namespaces := range []map[string]string{
		{"dc": "urn:conflict"},
		{"": "urn:default"},
		{"bad:name": "urn:custom"},
		{"xml": "urn:custom"},
		{"xmlns": "urn:custom"},
		{"cdr": "relative"},
		{"cdr": "urn:with space"},
		{"cdr": xmlNamespace},
		{"cdr": "http://www.w3.org/2000/xmlns/"},
	} {
		if err := WriteMetadataToFile(path, &Information{Namespaces: namespaces}); err == nil {
			t.Fatalf("accepted invalid namespace update: %+v", namespaces)
		}
		if !bytes.Equal(before, readWriteTestFile(t, path)) {
			t.Fatal("invalid namespace update modified original")
		}
	}
}
