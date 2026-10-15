package epub

import (
	"archive/zip"
	"bytes"
	"encoding/json"
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

func TestInformationUpdateJSON(t *testing.T) {
	update := Information{
		Title: []string{"New title"}, Series: "Series", SeriesIndex: "2",
	}
	data, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	var got Information
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, update) {
		t.Fatalf("metadata did not round trip: %s", data)
	}
	if err := json.Unmarshal([]byte(`{"Subject":[]}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Subject == nil || len(got.Subject) != 0 {
		t.Fatal("empty JSON array did not preserve an explicit clear")
	}
	var omitted Information
	if err := json.Unmarshal([]byte(`{}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if omitted.Title != nil || omitted.Series != "" || omitted.SeriesIndex != "" {
		t.Fatalf("omitted fields are not nil: %+v", omitted)
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

func TestWriteMetadataPartialUpdate(t *testing.T) {
	for _, version := range []string{"2.0", "3.0"} {
		t.Run(version, func(t *testing.T) {
			original := strings.ReplaceAll(writeTestPackage, "VERSION", version)
			path := createWriteTestEPUB(t, original)
			before, err := zip.OpenReader(path)
			if err != nil {
				t.Fatal(err)
			}
			defer before.Close()
			title := `New <Title> & "more"`
			if err := WriteMetadataToFile(path, &Information{
				Title: []string{title}, Subject: []string{},
			}); err != nil {
				t.Fatal(err)
			}
			info, err := GetMetadataFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(info.Title, []string{title}) || len(info.Subject) != 0 {
				t.Fatalf("updates not applied: %+v", info)
			}
			if !reflect.DeepEqual(info.SubTitle, []string{"Original subtitle"}) ||
				info.Creator[0].FullName != "Original author" || info.Series != "Old series" {
				t.Fatalf("untouched fields changed: %+v", info)
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
		})
	}
}

func TestWriteMetadataAllFields(t *testing.T) {
	for _, version := range []string{"2.0", "3.0"} {
		t.Run(version, func(t *testing.T) {
			path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", version))
			series, index := "New series", "2.5"
			u := &Information{
				Identifier: []Identifier{{Scheme: "ISBN", Value: "978-test"}},
				Title:      []string{"Title", "Another title"}, SubTitle: []string{"Subtitle"},
				Language: []string{"es", "en"}, Contributor: []Author{{FullName: "Translator", Role: "trl", FileAs: "Translator"}},
				Creator:  []Author{{FullName: "New author", Role: "aut", FileAs: "Author, New"}},
				Date:     []Date{{Stamp: "2026-10-15", Event: "publication"}},
				Coverage: []string{"Coverage"}, Description: []string{"Description"},
				Format: []string{"Format"}, Publisher: []string{"Publisher"},
				Relation: []string{"Relation"}, Rights: []string{"Rights"},
				Source: []string{"Source"}, Subject: []string{"Subject"}, Type: []string{"Type"},
				Meta:   []GenericMetadata{{Name: "vendor:new", Content: "new content"}},
				Series: series, SeriesIndex: index,
			}
			if err := WriteMetadataToFile(path, u); err != nil {
				t.Fatal(err)
			}
			got, err := GetMetadataFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := *u
			want.Series, want.SeriesIndex = series, index
			if version == "2.0" {
				want.Meta = append(append([]GenericMetadata(nil), want.Meta...),
					GenericMetadata{Name: "calibre:series", Content: series},
					GenericMetadata{Name: "calibre:series_index", Content: index})
			}
			if !reflect.DeepEqual(got, &want) {
				t.Fatalf("round trip mismatch:\ngot:  %+v\nwant: %+v", got, want)
			}
			if err := WriteMetadataToFile(path, u); err != nil {
				t.Fatalf("repeated update: %v", err)
			}
			if err := WriteMetadataToFile(path, &Information{
				SubTitle: []string{}, Meta: []GenericMetadata{},
			}, "Series"); err != nil {
				t.Fatal(err)
			}
			got, err = GetMetadataFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got.Series != "" || got.SeriesIndex != "" || len(got.SubTitle) != 0 || len(got.Meta) != 0 {
				t.Fatalf("clear failed: %+v", got)
			}
		})
	}
}

func TestWriteMetadataFailuresPreserveOriginal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pkg    string
		update *Information
	}{
		{"nil update", writeTestPackage, nil},
		{"remove primary identifier", writeTestPackage, &Information{Identifier: []Identifier{}}},
		{"malformed XML", "<package>", &Information{Title: []string{"Title"}}},
		{"missing metadata", `<package xmlns="http://www.idpf.org/2007/opf"/>`, &Information{Title: []string{"Title"}}},
		{"invalid XML character", writeTestPackage, &Information{Title: []string{"bad\x00title"}}},
		{"invalid UTF-8", writeTestPackage, &Information{Title: []string{"bad\xfftitle"}}},
		{"invalid attribute", writeTestPackage, &Information{Creator: []Author{{FullName: "Author", FileAs: "\x00"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := createWriteTestEPUB(t, strings.ReplaceAll(tc.pkg, "VERSION", "3.0"))
			before := readWriteTestFile(t, path)
			if err := WriteMetadataToFile(path, tc.update); err == nil {
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

func TestWriteMetadataSeriesRefinements(t *testing.T) {
	pkg := strings.ReplaceAll(writeTestPackage, "VERSION", "3.0")
	pkg = strings.Replace(pkg, `<meta name="calibre:series" content="Old series"/>`, `
<meta property="belongs-to-collection" id="series">Old series</meta>
<meta property="collection-type" refines="#series">series</meta>
<meta property="group-position" refines="#series">1</meta>
<meta property="belongs-to-collection" id="other-collection">Other collection</meta>
<meta property="collection-type" refines="#other-collection">set</meta>`, 1)
	path := createWriteTestEPUB(t, pkg)
	index := "4"
	if err := WriteMetadataToFile(path, &Information{SeriesIndex: index}); err != nil {
		t.Fatal(err)
	}
	info, err := GetMetadataFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Series != "Old series" || info.SeriesIndex != "4" {
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
	info, err = GetMetadataFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Series != "" || info.SeriesIndex != "" {
		t.Fatalf("unrelated set treated as series after clearing: %+v", info)
	}
}

func TestWriteMetadataClearFields(t *testing.T) {
	for _, version := range []string{"2.0", "3.0"} {
		t.Run(version, func(t *testing.T) {
			path := createWriteTestEPUB(t, strings.ReplaceAll(writeTestPackage, "VERSION", version))
			before := readWriteTestFile(t, path)
			if err := WriteMetadataToFile(path, &Information{}, "Unknown"); err == nil {
				t.Fatal("expected unknown clear field error")
			}
			if !bytes.Equal(before, readWriteTestFile(t, path)) {
				t.Fatal("invalid clear list modified original")
			}
			if err := WriteMetadataToFile(path, &Information{Series: "", SeriesIndex: ""}); err != nil {
				t.Fatal(err)
			}
			info, err := GetMetadataFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Series != "Old series" || info.SeriesIndex != "1" {
				t.Fatalf("empty strings changed series: %+v", info)
			}
			if err := WriteMetadataToFile(path, &Information{SeriesIndex: "9"}, "SeriesIndex"); err != nil {
				t.Fatal(err)
			}
			info, err = GetMetadataFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Series != "Old series" || info.SeriesIndex != "" {
				t.Fatalf("index clear changed series or failed to override update: %+v", info)
			}
			if err := WriteMetadataToFile(path, &Information{Series: "Replacement", SeriesIndex: "9"}, "Series"); err != nil {
				t.Fatal(err)
			}
			info, err = GetMetadataFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Series != "" || info.SeriesIndex != "" {
				t.Fatalf("series clear did not override update: %+v", info)
			}
		})
	}
}

func TestWriteMetadataModifiedAndIDs(t *testing.T) {
	pkg := strings.ReplaceAll(writeTestPackage, "VERSION", "3.0")
	path := createWriteTestEPUB(t, pkg)
	if err := WriteMetadataToFile(path, &Information{Publisher: []string{"New publisher"}}); err != nil {
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
	if err := WriteMetadataToFile(path, &Information{Title: []string{"New title"}}); err != nil {
		t.Fatal(err)
	}
	info, err := GetMetadataFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(info.Title, []string{"New title"}) {
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
	if err := WriteMetadataToFile(link, &Information{Title: []string{"New"}}); err == nil {
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
			before, err := GetMetadataFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteMetadataToFile(path, &Information{Title: []string{"Updated title"}}); err != nil {
				t.Fatal(err)
			}
			after, err := GetMetadataFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			before.Title = []string{"Updated title"}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("unexpected metadata changes:\ngot: %+v\nwant: %+v", after, before)
			}
		})
	}
}
