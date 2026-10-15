package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	dcNamespace  = "http://purl.org/dc/elements/1.1/"
	opfNamespace = "http://www.idpf.org/2007/opf"
)

// WriteMetadataToFile applies updates to the default rendition of an EPUB.
// Nil slices and empty strings leave fields unchanged; non-nil empty slices
// clear fields. clearFields accepts "Series" and "SeriesIndex" to explicitly
// clear those string fields, overriding any supplied values.
// Meta replaces legacy name/content metadata except Calibre series entries.
// Other metadata, package content, and ZIP entries are preserved. The original
// file is replaced by a completed temporary archive in the same directory.
// Existing Epub readers must be reopened to see the changes.
// Symbolic links are rejected rather than replaced.
func WriteMetadataToFile(path string, updates *Information, clearFields ...string) (err error) {
	if updates == nil {
		return fmt.Errorf("metadata updates must not be nil")
	}
	clear := make(map[string]bool)
	for _, field := range clearFields {
		switch field {
		case "Series", "SeriesIndex":
			clear[field] = true
		default:
			return fmt.Errorf("unsupported clear field %q: use Series or SeriesIndex; clear list fields with empty slices", field)
		}
	}
	stat, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() {
		return fmt.Errorf("EPUB must be a regular file: %s", path)
	}
	e, err := Open(path)
	if err != nil {
		return err
	}
	defer e.Close()

	r, err := e.Open(e.rootfile)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	data, err = updatePackageMetadata(data, updates, clear)
	if err != nil {
		return fmt.Errorf("update package metadata: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".epub-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		tmp.Close()
		if removeErr := os.Remove(tmp.Name()); removeErr != nil && !os.IsNotExist(removeErr) && err == nil {
			err = removeErr
		}
	}()
	if err = tmp.Chmod(stat.Mode().Perm()); err != nil {
		return err
	}
	w := zip.NewWriter(tmp)
	if err = w.SetComment(e.Comment); err != nil {
		return err
	}
	for _, f := range e.File {
		if f.Name != e.rootfile {
			if err = w.Copy(f); err != nil {
				return fmt.Errorf("copy %s: %w", f.Name, err)
			}
			continue
		}
		header := f.FileHeader
		// CreateHeader recalculates CRCs and sizes; stale ZIP64 sizes must not
		// be carried over when replacing the package document.
		header.Extra = nil
		dst, createErr := w.CreateHeader(&header)
		if createErr != nil {
			return createErr
		}
		if _, err = dst.Write(data); err != nil {
			return err
		}
	}
	if err = w.Close(); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = e.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

type metadataChild struct {
	start, end int
	name       xml.Name
	id         string
	refines    string
	property   string
	legacyName string
	subtitle   bool
}

type packageXML struct {
	children []metadataChild
	end      int
	ids      map[string]bool
}

// Byte offsets let us retain XML that the public package structs do not model,
// including vendor extensions, namespace declarations, and EPUB 2 guides.
func scanPackageXML(data []byte) (*packageXML, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Entity = xml.HTMLEntity
	p := &packageXML{ids: make(map[string]bool)}
	depth, metadataDepth, end := 0, 0, -1
	for {
		start := int(d.InputOffset())
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			for _, a := range t.Attr {
				if a.Name == (xml.Name{Local: "id"}) {
					p.ids[a.Value] = true
				}
			}
			if depth == 2 && t.Name == (xml.Name{Space: opfNamespace, Local: "metadata"}) {
				if metadataDepth != 0 {
					return nil, fmt.Errorf("multiple metadata elements")
				}
				metadataDepth = depth
			} else if metadataDepth != 0 && depth == metadataDepth+1 && end == -1 {
				child := metadataChild{start: start, name: t.Name}
				for _, a := range t.Attr {
					if a.Name.Space != "" {
						continue
					}
					switch a.Name.Local {
					case "id":
						child.id = a.Value
					case "refines":
						child.refines = a.Value
					case "property":
						child.property = a.Value
					case "name":
						child.legacyName = a.Value
					}
				}
				p.children = append(p.children, child)
			}
		case xml.EndElement:
			if metadataDepth != 0 && end == -1 {
				if depth == metadataDepth+1 {
					p.children[len(p.children)-1].end = int(d.InputOffset())
				} else if depth == metadataDepth {
					end = start
				}
			}
			depth--
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("package has no metadata element")
	}
	p.end = end
	return p, nil
}

func encodeMetadata(tokens []xml.Token) ([]byte, error) {
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	for _, token := range tokens {
		switch t := token.(type) {
		case xml.CharData:
			if !validXMLText(string(t)) {
				return nil, fmt.Errorf("invalid XML text in metadata")
			}
		case xml.StartElement:
			for _, a := range t.Attr {
				if !validXMLText(a.Value) {
					return nil, fmt.Errorf("invalid XML text in %s attribute %s", t.Name.Local, a.Name.Local)
				}
			}
		}
		if err := enc.EncodeToken(token); err != nil {
			return nil, err
		}
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func updatePackageMetadata(data []byte, u *Information, clear map[string]bool) ([]byte, error) {
	pkg, err := newPackageDocument(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	source, err := scanPackageXML(data)
	if err != nil {
		return nil, err
	}
	children := source.children
	markSubtitles(children, pkg.Metadata.Meta)
	editor := metadataEditor{
		source: source, removed: make([]bool, len(children)),
		epub3: strings.HasPrefix(pkg.Version, "3"),
	}
	editor.updateTextFields(u)
	editor.updateIdentifiers(u.Identifier, pkg.UniqueIdentifier)
	editor.updateAuthors("creator", u.Creator)
	editor.updateAuthors("contributor", u.Contributor)
	editor.updateDates(u.Date)
	editor.updateLegacyMetadata(u.Meta)
	if err := editor.updateSeries(u, clear, pkg.Metadata.Meta); err != nil {
		return nil, err
	}
	editor.refreshModifiedTime()
	editor.removeRefinements()
	updated, err := editor.apply(data)
	if err != nil {
		return nil, err
	}
	p, err := newPackageDocument(bytes.NewReader(updated))
	if err != nil {
		return nil, fmt.Errorf("invalid updated package: %w", err)
	}
	if u.Identifier != nil && p.UniqueIdentifier != "" {
		for _, id := range p.Metadata.Identifier {
			if id.ID == p.UniqueIdentifier {
				return updated, nil
			}
		}
		return nil, fmt.Errorf("cannot remove the package's primary identifier")
	}
	return updated, nil
}

func markSubtitles(children []metadataChild, meta []MetaLegacy) {
	for i := range children {
		c := &children[i]
		if c.name.Space != dcNamespace || c.name.Local != "title" || c.id == "" {
			continue
		}
		for _, m := range meta {
			if m.Meta != nil && m.Refines == "#"+c.id && m.Property == "title-type" && m.Value == "subtitle" {
				c.subtitle = true
			}
		}
	}
}

func validXMLText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r != '\t' && r != '\n' && r != '\r' &&
			(r < 0x20 || r == 0xFFFE || r == 0xFFFF) {
			return false
		}
	}
	return true
}
