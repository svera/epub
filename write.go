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

// MetadataUpdate describes a partial update to an EPUB's metadata.
// Nil slices and pointers leave fields unchanged; empty, non-nil slices remove
// fields. Series and SeriesIndex can be cleared with pointers to empty strings.
// Meta updates legacy name/content metadata, excluding Calibre series fields,
// which are controlled by Series and SeriesIndex.
type MetadataUpdate struct {
	Identifier  []Identifier
	Title       []string
	SubTitle    []string
	Language    []string
	Contributor []Author
	Coverage    []string
	Creator     []Author
	Date        []Date
	Description []string
	Format      []string
	Publisher   []string
	Relation    []string
	Rights      []string
	Source      []string
	Subject     []string
	Type        []string
	Meta        []GenericMetadata
	Series      *string
	SeriesIndex *string
}

// WriteMetadataToFile applies updates to the default rendition of an EPUB.
// Other metadata, package content, and ZIP entries are preserved. The original
// file is replaced by a completed temporary archive in the same directory.
// Existing Epub readers must be reopened to see the changes.
// Symbolic links are rejected rather than replaced.
func WriteMetadataToFile(path string, updates *MetadataUpdate) (err error) {
	if updates == nil {
		return fmt.Errorf("metadata updates must not be nil")
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
	data, err = updatePackageMetadata(data, updates)
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

type metadataElement struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Value   string     `xml:",chardata"`
}

func encodeMetadata(elements []metadataElement) ([]byte, error) {
	for _, element := range elements {
		if !validXMLText(element.Value) {
			return nil, fmt.Errorf("invalid XML text in %s", element.XMLName.Local)
		}
		for _, a := range element.Attrs {
			if !validXMLText(a.Value) {
				return nil, fmt.Errorf("invalid XML text in %s attribute %s", element.XMLName.Local, a.Name.Local)
			}
		}
	}
	return xml.Marshal(elements)
}

func updatePackageMetadata(data []byte, u *MetadataUpdate) ([]byte, error) {
	pkg, err := newPackageDocument(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	source, err := scanPackageXML(data)
	if err != nil {
		return nil, err
	}
	children := source.children
	epub3 := strings.HasPrefix(pkg.Version, "3")
	for i := range children {
		c := &children[i]
		if c.name.Space != dcNamespace || c.name.Local != "title" || c.id == "" {
			continue
		}
		for _, m := range pkg.Metadata.Meta {
			if m.Meta != nil && m.Refines == "#"+c.id && m.Property == "title-type" && m.Value == "subtitle" {
				c.subtitle = true
			}
		}
	}

	removed := make([]bool, len(children))
	newID := func() string {
		for i := 1; ; i++ {
			id := fmt.Sprintf("epub-metadata-%d", i)
			if !source.ids[id] {
				source.ids[id] = true
				return id
			}
		}
	}
	var elements []metadataElement
	addElement := func(space, name, value string, attrs ...xml.Attr) {
		elements = append(elements, metadataElement{
			XMLName: xml.Name{Space: space, Local: name}, Attrs: attrs, Value: value,
		})
	}
	attr := func(name, value string) xml.Attr {
		return xml.Attr{Name: xml.Name{Local: name}, Value: value}
	}
	addMeta := func(id, property, refines, value string) {
		attrs := []xml.Attr{attr("property", property)}
		if id != "" {
			attrs = append(attrs, attr("id", id))
		}
		if refines != "" {
			attrs = append(attrs, attr("refines", "#"+refines))
		}
		addElement(opfNamespace, "meta", value, attrs...)
	}
	replace := func(name string, subtitle bool) []string {
		var ids []string
		for i, c := range children {
			if c.name == (xml.Name{Space: dcNamespace, Local: name}) && (name != "title" || c.subtitle == subtitle) {
				removed[i] = true
				ids = append(ids, c.id)
			}
		}
		return ids
	}
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
	elementID := func(ids []string, i int) string {
		if i < len(ids) && ids[i] != "" {
			return ids[i]
		}
		return newID()
	}
	for _, f := range fields {
		if f.values == nil {
			continue
		}
		ids := replace(f.name, f.subtitle)
		for i, value := range f.values {
			id := elementID(ids, i)
			addElement(dcNamespace, f.name, value, attr("id", id))
			if f.subtitle {
				addMeta("", "title-type", id, "subtitle")
			}
		}
	}
	if u.Identifier != nil {
		ids := replace("identifier", false)
		// Keep the package's primary identifier reference valid even when
		// the original primary identifier was not the first one.
		for i, id := range ids {
			if id == pkg.UniqueIdentifier {
				ids[0], ids[i] = ids[i], ids[0]
				break
			}
		}
		for i, value := range u.Identifier {
			id := elementID(ids, i)
			attrs := []xml.Attr{attr("id", id)}
			if value.Scheme != "" {
				attrs = append(attrs, xml.Attr{Name: xml.Name{Space: opfNamespace, Local: "scheme"}, Value: value.Scheme})
			}
			addElement(dcNamespace, "identifier", value.Value, attrs...)
		}
	}
	for _, f := range []struct {
		name   string
		values []Author
	}{{"creator", u.Creator}, {"contributor", u.Contributor}} {
		if f.values == nil {
			continue
		}
		ids := replace(f.name, false)
		for i, a := range f.values {
			id := elementID(ids, i)
			attrs := []xml.Attr{attr("id", id)}
			for _, v := range []struct{ name, value string }{{"role", a.Role}, {"file-as", a.FileAs}} {
				if v.value == "" {
					continue
				}
				if epub3 {
					addMeta("", v.name, id, v.value)
				} else {
					attrs = append(attrs, xml.Attr{Name: xml.Name{Space: opfNamespace, Local: v.name}, Value: v.value})
				}
			}
			addElement(dcNamespace, f.name, a.FullName, attrs...)
		}
	}
	if u.Date != nil {
		ids := replace("date", false)
		for i, value := range u.Date {
			attrs := []xml.Attr{attr("id", elementID(ids, i))}
			if value.Event != "" {
				attrs = append(attrs, xml.Attr{Name: xml.Name{Space: opfNamespace, Local: "event"}, Value: value.Event})
			}
			addElement(dcNamespace, "date", value.Stamp, attrs...)
		}
	}
	isSeries := func(name string) bool {
		return name == "calibre:series" || name == "calibre:series_index"
	}
	if u.Meta != nil {
		for i, c := range children {
			if c.name == (xml.Name{Space: opfNamespace, Local: "meta"}) && c.legacyName != "" && !isSeries(c.legacyName) {
				removed[i] = true
			}
		}
		for _, value := range u.Meta {
			if isSeries(value.Name) {
				continue
			}
			addElement(opfNamespace, "meta", "", attr("name", value.Name), attr("content", value.Content))
		}
	}
	if u.Series != nil || u.SeriesIndex != nil {
		series, index := getSeries(pkg.Metadata.Meta)
		if u.Series != nil {
			series = *u.Series
		}
		if u.SeriesIndex != nil {
			index = *u.SeriesIndex
		}
		if series == "" && index != "" && u.Series == nil {
			return nil, fmt.Errorf("series index requires a series name")
		}
		collectionID := ""
		collectionFound := false
		collection := seriesCollection(pkg.Metadata.Meta)
		for i, c := range children {
			if c.name != (xml.Name{Space: opfNamespace, Local: "meta"}) {
				continue
			}
			if isSeries(c.legacyName) {
				removed[i] = true
			}
			if collection != nil && c.property == "belongs-to-collection" && c.id == collection.ID && !collectionFound {
				collectionFound = true
				collectionID = c.id
				removed[i] = true
			}
		}
		if epub3 {
			if series != "" {
				if collectionID == "" {
					collectionID = newID()
				}
				addMeta(collectionID, "belongs-to-collection", "", series)
				addMeta("", "collection-type", collectionID, "series")
				if index != "" {
					addMeta("", "group-position", collectionID, index)
				}
			}
		} else {
			for _, v := range []struct{ name, value string }{{"calibre:series", series}, {"calibre:series_index", index}} {
				if v.value != "" && (v.name != "calibre:series_index" || series != "") {
					addElement(opfNamespace, "meta", "", attr("name", v.name), attr("content", v.value))
				}
			}
		}
	}
	changed := len(elements) != 0
	for _, remove := range removed {
		changed = changed || remove
	}
	if changed && epub3 {
		for i, c := range children {
			if c.name == (xml.Name{Space: opfNamespace, Local: "meta"}) && c.property == "dcterms:modified" && c.refines == "" {
				removed[i] = true
			}
		}
		addMeta("", "dcterms:modified", "", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	}
	// Remove refinements of replaced elements, including chained refinements.
	for changed := true; changed; {
		changed = false
		for i, c := range children {
			if removed[i] || c.refines == "" {
				continue
			}
			for j, target := range children {
				if removed[j] && target.id != "" && c.refines == "#"+target.id {
					removed[i], changed = true, true
					break
				}
			}
		}
	}
	generated, err := encodeMetadata(elements)
	if err != nil {
		return nil, err
	}
	var result bytes.Buffer
	offset := 0
	inserted := false
	for i, c := range children {
		if removed[i] {
			result.Write(data[offset:c.start])
			if !inserted {
				result.Write(generated)
				inserted = true
			}
			offset = c.end
		}
	}
	result.Write(data[offset:source.end])
	if !inserted {
		result.Write(generated)
	}
	result.Write(data[source.end:])
	updated := result.Bytes()
	p, err := newPackageDocument(bytes.NewReader(updated))
	if err != nil {
		return nil, fmt.Errorf("invalid updated package: %w", err)
	}
	if u.Identifier != nil && p.UniqueIdentifier != "" {
		found := false
		for _, id := range p.Metadata.Identifier {
			found = found || id.ID == p.UniqueIdentifier
		}
		if !found {
			return nil, fmt.Errorf("cannot remove the package's primary identifier")
		}
	}
	return updated, nil
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
