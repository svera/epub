package epub

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/beevik/etree"
)

// metadataEditor holds edit state, not another representation of the metadata.
type metadataEditor struct {
	source               *packageEditState
	removed              []bool
	elements             []metadataElement
	epub3                bool
	preservedRefinements map[string]bool
}

func (e *metadataEditor) prepareRefinements(groups [][]Meta) error {
	for _, values := range groups {
		if len(values) != 0 && !e.epub3 {
			return fmt.Errorf("metadata refinements require EPUB 3; use Attributes for custom EPUB 2 attributes")
		}
		for _, m := range values {
			if strings.TrimSpace(m.Property) == "" {
				return fmt.Errorf("metadata refinement requires a property")
			}
			if m.ID != "" {
				e.source.ids[m.ID] = true
			}
		}
	}
	return nil
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
	e.elements = append(e.elements, metadataElement{
		name: xml.Name{Space: space, Local: name}, value: value, attributes: attrs,
	})
}

func attr(name, value string) xml.Attr {
	return xml.Attr{Name: xml.Name{Local: name}, Value: value}
}

func (e *metadataEditor) addMeta(id, property, refines, value string, attributes ...xml.Attr) {
	attrs := []xml.Attr{attr("property", property)}
	if id != "" {
		attrs = append(attrs, attr("id", id))
	}
	if refines != "" {
		attrs = append(attrs, attr("refines", "#"+refines))
	}
	e.addElement(opfNamespace, "meta", value, append(attrs, attributes...)...)
}

func (e *metadataEditor) replace(name string, subtitle bool) []metadataChild {
	var previous []metadataChild
	for i, c := range e.source.children {
		if c.name == (xml.Name{Space: dcNamespace, Local: name}) && (name != "title" || c.subtitle == subtitle) {
			e.removed[i] = true
			previous = append(previous, c)
		}
	}
	return previous
}

func (e *metadataEditor) elementID(previous []metadataChild, i int) string {
	if i < len(previous) && previous[i].id != "" {
		return previous[i].id
	}
	return e.newID()
}

func (e *metadataEditor) updateTextFields(u, existing *Information) error {
	type field struct {
		name     string
		values   []MetadataValue
		existing []MetadataValue
		subtitle bool
	}
	fields := []field{
		{"title", u.Title, existing.Title, false}, {"title", u.SubTitle, existing.SubTitle, true},
		{"language", u.Language, existing.Language, false}, {"coverage", u.Coverage, existing.Coverage, false},
		{"description", u.Description, existing.Description, false}, {"format", u.Format, existing.Format, false},
		{"publisher", u.Publisher, existing.Publisher, false}, {"relation", u.Relation, existing.Relation, false},
		{"rights", u.Rights, existing.Rights, false}, {"source", u.Source, existing.Source, false},
		{"subject", u.Subject, existing.Subject, false}, {"type", u.Type, existing.Type, false},
	}
	for _, f := range fields {
		if f.values == nil {
			continue
		}
		previous := e.replace(f.name, f.subtitle)
		for i, value := range f.values {
			preserveRefinements := value.Value == nil && value.Refinements == nil
			if value.Value == nil {
				if i >= len(f.existing) {
					return fmt.Errorf("%s value %d omits Value but has no existing text", f.name, i+1)
				}
				value.Value = f.existing[i].Value
				if value.Attributes == nil {
					for _, a := range previous[i].attributes {
						if a.Name.Space == "xmlns" || a.Name.Space == "http://www.w3.org/2000/xmlns/" ||
							(a.Name.Space == "" && (a.Name.Local == "id" || a.Name.Local == "xmlns")) {
							continue
						}
						value.Attributes = append(value.Attributes, a)
					}
				}
			}
			id := e.elementID(previous, i)
			e.addElement(dcNamespace, f.name, value.String(), append([]xml.Attr{attr("id", id)}, value.Attributes...)...)
			if preserveRefinements {
				if e.preservedRefinements == nil {
					e.preservedRefinements = make(map[string]bool)
				}
				e.preservedRefinements[id] = true
			} else {
				e.addRefinements(id, value.Refinements)
				if f.subtitle && !hasProperty(value.Refinements, "title-type") {
					e.addMeta("", "title-type", id, "subtitle")
				}
			}
		}
	}
	return nil
}

func (e *metadataEditor) updateIdentifiers(values []Identifier, primaryID string) {
	if values != nil {
		previous := e.replace("identifier", false)
		// Keep the package's primary identifier reference valid even when
		// the original primary identifier was not the first one.
		for i, child := range previous {
			if child.id == primaryID {
				previous[0], previous[i] = previous[i], previous[0]
				break
			}
		}
		for i, value := range values {
			id := e.elementID(previous, i)
			attrs := []xml.Attr{attr("id", id)}
			if value.Scheme != "" {
				attrs = append(attrs, xml.Attr{Name: xml.Name{Space: opfNamespace, Local: "scheme"}, Value: value.Scheme})
			}
			e.addElement(dcNamespace, "identifier", value.Value, append(attrs, value.Attributes...)...)
			e.addRefinements(id, value.Refinements)
		}
	}
}

func (e *metadataEditor) updateAuthors(name string, values []Author) {
	if values == nil {
		return
	}
	previous := e.replace(name, false)
	for i, a := range values {
		id := e.elementID(previous, i)
		attrs := []xml.Attr{attr("id", id)}
		for _, v := range []struct{ name, value string }{{"role", a.Role}, {"file-as", a.FileAs}} {
			if v.value == "" || hasProperty(a.Refinements, v.name) {
				continue
			}
			if e.epub3 {
				e.addMeta("", v.name, id, v.value)
			} else {
				attrs = append(attrs, xml.Attr{Name: xml.Name{Space: opfNamespace, Local: v.name}, Value: v.value})
			}
		}
		e.addElement(dcNamespace, name, a.FullName, append(attrs, a.Attributes...)...)
		e.addRefinements(id, a.Refinements)
	}
}

func (e *metadataEditor) addRefinements(targetID string, values []Meta) {
	for _, m := range values {
		attrs := []xml.Attr{attr("property", m.Property), attr("refines", "#"+targetID)}
		for _, a := range []xml.Attr{
			attr("id", m.ID), attr("scheme", m.Scheme), attr("dir", m.Dir),
			{Name: xml.Name{Space: "http://www.w3.org/XML/1998/namespace", Local: "lang"}, Value: m.Lang},
		} {
			if a.Value != "" {
				attrs = append(attrs, a)
			}
		}
		e.addElement(opfNamespace, "meta", m.Value, append(attrs, m.Attributes...)...)
	}
}

func hasProperty(values []Meta, property string) bool {
	for _, m := range values {
		if m.Property == property {
			return true
		}
	}
	return false
}

func (e *metadataEditor) updateDates(values []Date) {
	if values != nil {
		previous := e.replace("date", false)
		for i, value := range values {
			id := e.elementID(previous, i)
			attrs := []xml.Attr{attr("id", id)}
			if value.Event != "" {
				attrs = append(attrs, xml.Attr{Name: xml.Name{Space: opfNamespace, Local: "event"}, Value: value.Event})
			}
			e.addElement(dcNamespace, "date", value.Stamp, append(attrs, value.Attributes...)...)
			e.addRefinements(id, value.Refinements)
		}
	}
}

func isSeries(name string) bool {
	return name == "calibre:series" || name == "calibre:series_index"
}

func (e *metadataEditor) updateLegacyMetadata(values []GenericMetadata) error {
	if values != nil {
		for i, c := range e.source.children {
			if c.name == (xml.Name{Space: opfNamespace, Local: "meta"}) && c.legacyName != "" &&
				!isSeries(c.legacyName) {
				e.removed[i] = true
			}
		}
		for _, value := range values {
			if isSeries(value.Name) {
				if len(value.Refinements) != 0 {
					return fmt.Errorf("use Series or SeriesIndex to refine %q", value.Name)
				}
				continue
			}
			attrs := []xml.Attr{attr("name", value.Name), attr("content", value.Content)}
			id := ""
			if len(value.Refinements) != 0 {
				id = e.newID()
				attrs = append(attrs, attr("id", id))
			}
			e.addElement(opfNamespace, "meta", "", append(attrs, value.Attributes...)...)
			e.addRefinements(id, value.Refinements)
		}
	}
	return nil
}

func (e *metadataEditor) updateSeries(u *Information, clear map[string]bool, meta []MetaLegacy) error {
	if u.Series.omitted() && u.SeriesIndex.omitted() &&
		!clear["Series"] && !clear["SeriesIndex"] {
		return nil
	}
	oldSeries, oldIndex := seriesValues(meta)
	seriesValue, indexValue := u.Series.withExisting(oldSeries), u.SeriesIndex.withExisting(oldIndex)
	series, index := seriesValue.String(), indexValue.String()
	seriesRefinements, indexRefinements := seriesValue.Refinements, indexValue.Refinements
	seriesAttributes, indexAttributes := seriesValue.Attributes, indexValue.Attributes
	clearSeries := clear["Series"] || (u.Series.Value != nil && *u.Series.Value == "")
	clearIndex := clear["SeriesIndex"] || (u.SeriesIndex.Value != nil && *u.SeriesIndex.Value == "")
	if clearSeries {
		series, index = "", ""
	}
	if clearIndex {
		index = ""
		filtered := make([]Meta, 0, len(seriesRefinements))
		for _, m := range seriesRefinements {
			if m.Property != "group-position" {
				filtered = append(filtered, m)
			}
		}
		seriesRefinements = filtered
	}
	if series == "" && index != "" {
		return fmt.Errorf("series index requires a series name")
	}
	if !clearSeries && series == "" && (len(u.Series.Refinements) != 0 || len(u.Series.Attributes) != 0) {
		return fmt.Errorf("series refinements or attributes require a series name")
	}
	if !clearSeries && !clearIndex && index == "" && (len(u.SeriesIndex.Refinements) != 0 || len(u.SeriesIndex.Attributes) != 0) {
		return fmt.Errorf("series index refinements or attributes require an index")
	}
	if !clearSeries && hasProperty(seriesRefinements, "group-position") &&
		(len(indexRefinements) != 0 || len(indexAttributes) != 0) {
		return fmt.Errorf("set SeriesIndex instead of a group-position refinement when refining the index or adding attributes")
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
			e.addMeta(collectionID, "belongs-to-collection", "", series, seriesAttributes...)
			e.addRefinements(collectionID, seriesRefinements)
			if !hasProperty(seriesRefinements, "collection-type") {
				e.addMeta("", "collection-type", collectionID, "series")
			}
			if index != "" && !hasProperty(seriesRefinements, "group-position") {
				indexID := ""
				if len(indexRefinements) != 0 {
					indexID = e.newID()
				}
				e.addMeta(indexID, "group-position", collectionID, index, indexAttributes...)
				e.addRefinements(indexID, indexRefinements)
			}
		}
	} else {
		for _, v := range []struct{ name, value string }{{"calibre:series", series}, {"calibre:series_index", index}} {
			if v.value != "" && (v.name != "calibre:series_index" || series != "") {
				attributes := seriesAttributes
				if v.name == "calibre:series_index" {
					attributes = indexAttributes
				}
				attrs := []xml.Attr{attr("name", v.name), attr("content", v.value)}
				e.addElement(opfNamespace, "meta", "", append(attrs, attributes...)...)
			}
		}
	}
	return nil
}

func (e *metadataEditor) refreshModifiedTime(prefixesChanged bool) {
	changed := prefixesChanged || len(e.elements) != 0
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
	removedTargets := make(map[string]bool)
	for i, c := range e.source.children {
		if e.removed[i] && c.id != "" && !e.preservedRefinements[c.id] {
			removedTargets["#"+c.id] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for i, c := range e.source.children {
			if e.removed[i] || !removedTargets[c.refines] {
				continue
			}
			e.removed[i], changed = true, true
			if c.id != "" && !e.preservedRefinements[c.id] {
				removedTargets["#"+c.id] = true
			}
		}
	}
}

func (e *metadataEditor) apply(namespaces *xmlNamespaces) ([]byte, error) {
	generated, err := buildMetadata(e.elements, namespaces)
	if err != nil {
		return nil, err
	}
	metadata := e.source.metadata
	insertAt := len(metadata.Child)
	for i, child := range e.source.children {
		if e.removed[i] {
			insertAt = child.element.Index()
			break
		}
	}
	for i, element := range generated {
		metadata.InsertChildAt(insertAt+i, element)
	}
	for i, child := range e.source.children {
		if e.removed[i] {
			metadata.RemoveChild(child.element)
		}
	}
	namespaces.declare(metadata)
	// etree serializes UTF-8 even when the original document used another encoding.
	for _, token := range e.source.document.Child {
		if instruction, ok := token.(*etree.ProcInst); ok && instruction.Target == "xml" {
			instruction.Inst = xmlEncoding.ReplaceAllString(instruction.Inst, `encoding="UTF-8"`)
		}
	}
	return e.source.document.WriteToBytes()
}
