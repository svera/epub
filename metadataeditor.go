package epub

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// metadataEditor holds edit state, not another representation of the metadata.
type metadataEditor struct {
	source   *packageXML
	removed  []bool
	elements []xml.Token
	epub3    bool
}

func (e *metadataEditor) prepareRefinements(groups [][]Meta) error {
	for _, values := range groups {
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
		values   []MetadataValue
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
			e.addElement(dcNamespace, f.name, value.Value, attr("id", id))
			e.addRefinements(id, value.Refinements)
			if f.subtitle && !hasProperty(value.Refinements, "title-type") {
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
			e.addRefinements(id, value.Refinements)
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
			if v.value == "" || hasProperty(a.Refinements, v.name) {
				continue
			}
			if e.epub3 {
				e.addMeta("", v.name, id, v.value)
			} else {
				attrs = append(attrs, xml.Attr{Name: xml.Name{Space: opfNamespace, Local: v.name}, Value: v.value})
			}
		}
		e.addElement(dcNamespace, name, a.FullName, attrs...)
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
		e.addElement(opfNamespace, "meta", m.Value, attrs...)
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
		ids := e.replace("date", false)
		for i, value := range values {
			id := e.elementID(ids, i)
			attrs := []xml.Attr{attr("id", id)}
			if value.Event != "" {
				attrs = append(attrs, xml.Attr{Name: xml.Name{Space: opfNamespace, Local: "event"}, Value: value.Event})
			}
			e.addElement(dcNamespace, "date", value.Stamp, attrs...)
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
				c.legacyName != legacyRefinementName && !isSeries(c.legacyName) {
				e.removed[i] = true
			}
		}
		for _, value := range values {
			if value.Name == legacyRefinementName {
				return fmt.Errorf("%s is reserved; use attached Refinements instead", legacyRefinementName)
			}
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
			e.addElement(opfNamespace, "meta", "", attrs...)
			e.addRefinements(id, value.Refinements)
		}
	}
	return nil
}

func (e *metadataEditor) updateSeries(u *Information, clear map[string]bool, meta []MetaLegacy) error {
	if u.Series.Value == "" && u.SeriesIndex.Value == "" && u.Series.Refinements == nil &&
		u.SeriesIndex.Refinements == nil && !clear["Series"] && !clear["SeriesIndex"] {
		return nil
	}
	series, index := getSeries(meta)
	oldSeries, oldIndex := seriesValues(meta)
	seriesRefinements, indexRefinements := u.Series.Refinements, u.SeriesIndex.Refinements
	if u.Series.Value == "" && seriesRefinements == nil {
		seriesRefinements = oldSeries.Refinements
	}
	if u.SeriesIndex.Value == "" && indexRefinements == nil {
		indexRefinements = oldIndex.Refinements
	}
	if u.Series.Value != "" {
		series = u.Series.Value
	}
	if u.SeriesIndex.Value != "" {
		index = u.SeriesIndex.Value
	}
	if clear["Series"] {
		series, index = "", ""
	}
	if clear["SeriesIndex"] {
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
	if !clear["Series"] && series == "" && len(u.Series.Refinements) != 0 {
		return fmt.Errorf("series refinements require a series name")
	}
	if !clear["Series"] && !clear["SeriesIndex"] && index == "" && len(u.SeriesIndex.Refinements) != 0 {
		return fmt.Errorf("series index refinements require an index")
	}
	if !clear["Series"] && hasProperty(seriesRefinements, "group-position") && len(indexRefinements) != 0 {
		return fmt.Errorf("set SeriesIndex instead of a group-position refinement when refining the index")
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
			e.addRefinements(collectionID, seriesRefinements)
			if !hasProperty(seriesRefinements, "collection-type") {
				e.addMeta("", "collection-type", collectionID, "series")
			}
			if index != "" {
				indexID := ""
				if len(indexRefinements) != 0 {
					indexID = e.newID()
				}
				if !hasProperty(seriesRefinements, "group-position") {
					e.addMeta(indexID, "group-position", collectionID, index)
					e.addRefinements(indexID, indexRefinements)
				}
			}
		}
	} else {
		for _, v := range []struct{ name, value string }{{"calibre:series", series}, {"calibre:series_index", index}} {
			if v.value != "" && (v.name != "calibre:series_index" || series != "") {
				refinements := seriesRefinements
				if v.name == "calibre:series_index" {
					refinements = indexRefinements
				}
				attrs := []xml.Attr{attr("name", v.name), attr("content", v.value)}
				id := ""
				if len(refinements) != 0 {
					id = e.newID()
					attrs = append(attrs, attr("id", id))
				}
				e.addElement(opfNamespace, "meta", "", attrs...)
				e.addRefinements(id, refinements)
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
	generated, err := encodeMetadata(e.elements, e.epub3)
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
