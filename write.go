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
	"time"
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

// metadataEditor holds edit state, not another representation of the metadata.
type metadataEditor struct {
	source   *packageXML
	removed  []bool
	elements []xml.Token
	epub3    bool
}

func (e *metadataEditor) newID() string {
	for i := 1; ; i++ {
		id := fmt.Sprintf("epub-metadata-%d", i)
		if !e.source.ids[id] {
			e.source.ids[id] = true
			return id
		}
	}
}

func (e *metadataEditor) addElement(space, name, value string, attrs ...xml.Attr) {
	start := xml.StartElement{Name: xml.Name{Space: space, Local: name}, Attr: attrs}
	e.elements = append(e.elements, start, xml.CharData(value), start.End())
}

func attr(name, value string) xml.Attr {
	return xml.Attr{Name: xml.Name{Local: name}, Value: value}
}

func (e *metadataEditor) addMeta(id, property, refines, value string) {
	attrs := []xml.Attr{attr("property", property)}
	if id != "" {
		attrs = append(attrs, attr("id", id))
	}
	if refines != "" {
		attrs = append(attrs, attr("refines", "#"+refines))
	}
	e.addElement(opfNamespace, "meta", value, attrs...)
}

func (e *metadataEditor) replace(name string, subtitle bool) []string {
	var ids []string
	for i, c := range e.source.children {
		if c.name == (xml.Name{Space: dcNamespace, Local: name}) && (name != "title" || c.subtitle == subtitle) {
			e.removed[i] = true
			ids = append(ids, c.id)
		}
	}
	return ids
}

func (e *metadataEditor) elementID(ids []string, i int) string {
	if i < len(ids) && ids[i] != "" {
		return ids[i]
	}
	return e.newID()
}

func (e *metadataEditor) updateTextFields(u *Information) {
	type field struct {
		name     string
		values   []string
		subtitle bool
	}
	fields := []field{
		{"title", u.Title, false}, {"title", u.SubTitle, true},
		{"language", u.Language, false}, {"coverage", u.Coverage, false},
		{"description", u.Description, false}, {"format", u.Format, false},
		{"publisher", u.Publisher, false}, {"relation", u.Relation, false},
		{"rights", u.Rights, false}, {"source", u.Source, false},
		{"subject", u.Subject, false}, {"type", u.Type, false},
	}
	for _, f := range fields {
		if f.values == nil {
			continue
		}
		ids := e.replace(f.name, f.subtitle)
		for i, value := range f.values {
			id := e.elementID(ids, i)
			e.addElement(dcNamespace, f.name, value, attr("id", id))
			if f.subtitle {
				e.addMeta("", "title-type", id, "subtitle")
			}
		}
	}
}

func (e *metadataEditor) updateIdentifiers(values []Identifier, primaryID string) {
	if values != nil {
		ids := e.replace("identifier", false)
		// Keep the package's primary identifier reference valid even when
		// the original primary identifier was not the first one.
		for i, id := range ids {
			if id == primaryID {
				ids[0], ids[i] = ids[i], ids[0]
				break
			}
		}
		for i, value := range values {
			id := e.elementID(ids, i)
			attrs := []xml.Attr{attr("id", id)}
			if value.Scheme != "" {
				attrs = append(attrs, xml.Attr{Name: xml.Name{Space: opfNamespace, Local: "scheme"}, Value: value.Scheme})
			}
			e.addElement(dcNamespace, "identifier", value.Value, attrs...)
		}
	}
}

func (e *metadataEditor) updateAuthors(name string, values []Author) {
	if values == nil {
		return
	}
	ids := e.replace(name, false)
	for i, a := range values {
		id := e.elementID(ids, i)
		attrs := []xml.Attr{attr("id", id)}
		for _, v := range []struct{ name, value string }{{"role", a.Role}, {"file-as", a.FileAs}} {
			if v.value == "" {
				continue
			}
			if e.epub3 {
				e.addMeta("", v.name, id, v.value)
			} else {
				attrs = append(attrs, xml.Attr{Name: xml.Name{Space: opfNamespace, Local: v.name}, Value: v.value})
			}
		}
		e.addElement(dcNamespace, name, a.FullName, attrs...)
	}
}

func (e *metadataEditor) updateDates(values []Date) {
	if values != nil {
		ids := e.replace("date", false)
		for i, value := range values {
			attrs := []xml.Attr{attr("id", e.elementID(ids, i))}
			if value.Event != "" {
				attrs = append(attrs, xml.Attr{Name: xml.Name{Space: opfNamespace, Local: "event"}, Value: value.Event})
			}
			e.addElement(dcNamespace, "date", value.Stamp, attrs...)
		}
	}
}

func isSeries(name string) bool {
	return name == "calibre:series" || name == "calibre:series_index"
}

func (e *metadataEditor) updateLegacyMetadata(values []GenericMetadata) {
	if values != nil {
		for i, c := range e.source.children {
			if c.name == (xml.Name{Space: opfNamespace, Local: "meta"}) && c.legacyName != "" && !isSeries(c.legacyName) {
				e.removed[i] = true
			}
		}
		for _, value := range values {
			if isSeries(value.Name) {
				continue
			}
			e.addElement(opfNamespace, "meta", "", attr("name", value.Name), attr("content", value.Content))
		}
	}
}

func (e *metadataEditor) updateSeries(u *Information, clear map[string]bool, meta []MetaLegacy) error {
	if u.Series == "" && u.SeriesIndex == "" && !clear["Series"] && !clear["SeriesIndex"] {
		return nil
	}
	series, index := getSeries(meta)
	if u.Series != "" {
		series = u.Series
	}
	if u.SeriesIndex != "" {
		index = u.SeriesIndex
	}
	if clear["Series"] {
		series, index = "", ""
	}
	if clear["SeriesIndex"] {
		index = ""
	}
	if series == "" && index != "" {
		return fmt.Errorf("series index requires a series name")
	}
	collectionID := ""
	collectionFound := false
	collection := seriesCollection(meta)
	for i, c := range e.source.children {
		if c.name != (xml.Name{Space: opfNamespace, Local: "meta"}) {
			continue
		}
		if isSeries(c.legacyName) {
			e.removed[i] = true
		}
		if collection != nil && c.property == "belongs-to-collection" && c.id == collection.ID && !collectionFound {
			collectionFound = true
			collectionID = c.id
			e.removed[i] = true
		}
	}
	if e.epub3 {
		if series != "" {
			if collectionID == "" {
				collectionID = e.newID()
			}
			e.addMeta(collectionID, "belongs-to-collection", "", series)
			e.addMeta("", "collection-type", collectionID, "series")
			if index != "" {
				e.addMeta("", "group-position", collectionID, index)
			}
		}
	} else {
		for _, v := range []struct{ name, value string }{{"calibre:series", series}, {"calibre:series_index", index}} {
			if v.value != "" && (v.name != "calibre:series_index" || series != "") {
				e.addElement(opfNamespace, "meta", "", attr("name", v.name), attr("content", v.value))
			}
		}
	}
	return nil
}

func (e *metadataEditor) refreshModifiedTime() {
	changed := len(e.elements) != 0
	for _, remove := range e.removed {
		changed = changed || remove
	}
	if changed && e.epub3 {
		for i, c := range e.source.children {
			if c.name == (xml.Name{Space: opfNamespace, Local: "meta"}) && c.property == "dcterms:modified" && c.refines == "" {
				e.removed[i] = true
			}
		}
		e.addMeta("", "dcterms:modified", "", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	}
}

func (e *metadataEditor) removeRefinements() {
	// Remove refinements of replaced elements, including chained refinements.
	for changed := true; changed; {
		changed = false
		for i, c := range e.source.children {
			if e.removed[i] || c.refines == "" {
				continue
			}
			for j, target := range e.source.children {
				if e.removed[j] && target.id != "" && c.refines == "#"+target.id {
					e.removed[i], changed = true, true
					break
				}
			}
		}
	}
}

func (e *metadataEditor) apply(data []byte) ([]byte, error) {
	generated, err := encodeMetadata(e.elements)
	if err != nil {
		return nil, err
	}
	var result bytes.Buffer
	offset := 0
	inserted := false
	for i, c := range e.source.children {
		if e.removed[i] {
			result.Write(data[offset:c.start])
			if !inserted {
				result.Write(generated)
				inserted = true
			}
			offset = c.end
		}
	}
	result.Write(data[offset:e.source.end])
	if !inserted {
		result.Write(generated)
	}
	result.Write(data[e.source.end:])
	return result.Bytes(), nil
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
