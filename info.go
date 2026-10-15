package epub

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"slices"
)

// MetadataValue is a text value with optional refinements and XML attributes. The writer
// sets each refinement's Refines target automatically.
// Refinements require EPUB 3; Attributes works in both EPUB versions.
// In JSON, plain values are strings; values with refinements or attributes are objects.
// A nil Value preserves existing text when editing; a non-nil Value replaces it.
type MetadataValue struct {
	Value       *string    `json:",omitempty"`
	Refinements []Meta     `json:",omitempty"`
	Attributes  []xml.Attr `json:",omitempty"`
}

// NewMetadataValue creates a value with explicitly supplied text, including "".
func NewMetadataValue(value string) MetadataValue {
	return MetadataValue{Value: &value}
}

func (v MetadataValue) String() string {
	if v.Value == nil {
		return ""
	}
	return *v.Value
}

func (v MetadataValue) omitted() bool {
	return v.Value == nil && v.Refinements == nil && v.Attributes == nil
}

func (v MetadataValue) withExisting(existing MetadataValue) MetadataValue {
	if v.Value == nil {
		v.Value = existing.Value
		if v.Refinements == nil {
			v.Refinements = existing.Refinements
		}
		if v.Attributes == nil {
			v.Attributes = existing.Attributes
		}
	}
	return v
}

func (v MetadataValue) MarshalJSON() ([]byte, error) {
	if v.Value != nil && v.Refinements == nil && v.Attributes == nil {
		return json.Marshal(*v.Value)
	}
	var refinements *[]Meta
	var attributes *[]xml.Attr
	if v.Refinements != nil {
		refinements = &v.Refinements
	}
	if v.Attributes != nil {
		attributes = &v.Attributes
	}
	return json.Marshal(struct {
		Value       *string     `json:",omitempty"`
		Refinements *[]Meta     `json:",omitempty"`
		Attributes  *[]xml.Attr `json:",omitempty"`
	}{v.Value, refinements, attributes})
}

func (v *MetadataValue) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) != 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		*v = NewMetadataValue(text)
		return nil
	}
	type value MetadataValue
	var decoded value
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("invalid metadata value: %w", err)
	}
	*v = MetadataValue(decoded)
	return nil
}

// Information gathers meta information about an epub as a simpler version
// of Metadata to offer a more direct access to an Epub's metadata for simple
// use cases.
type Information struct {
	// Prefixes maps EPUB 3 vocabulary prefixes to absolute IRIs. Writes merge
	// declarations without replacing conflicting existing mappings.
	Prefixes map[string]string `json:",omitempty"`

	// Namespaces maps preferred XML prefixes to namespace URIs. Writes add
	// declarations on metadata and reuse inherited declarations in either EPUB version.
	// This is a write option, not returned by Information.
	Namespaces map[string]string `json:",omitempty"`

	// Identifier contains an identifier associated with the given
	// Rendition, such as a UUID, DOI or ISBN.
	Identifier []Identifier
	// Title represents the EPUB titles.
	Title []MetadataValue
	// SubTitle represents the EPUB sub-titles.
	SubTitle []MetadataValue `json:",omitempty"`

	// Language element specifies the language of the content of the
	// given Rendition.
	Language []MetadataValue

	// Contributor represents the name of a person, organization, etc.
	// that played a secondary role in the creation of the content of an
	// EPUB Publication.
	Contributor []Author `json:",omitempty"`
	// Coverage gives the extent or scope of the publication's content.
	Coverage []MetadataValue `json:",omitempty"`
	// Creator represents the name of a person, organization, etc.
	// responsible for the creation of the content of the Rendition.
	Creator []Author
	// Date lists events associated to the EPUB like publication, creation...
	Date []Date `json:",omitempty"`
	// Description provides a description of the publication's content.
	Description []MetadataValue `json:",omitempty"`
	// Format identifies the media type or dimensions of the resource.
	Format []MetadataValue `json:",omitempty"`
	// Publisher identifies the publication's publisher.
	Publisher []MetadataValue `json:",omitempty"`
	// Relation is an identifier of an auxiliary resource and its
	// relationship to the publication.
	Relation []MetadataValue `json:",omitempty"`
	// Rights provides a statement about rights, or a reference to one.
	Rights []MetadataValue `json:",omitempty"`
	// Sources provides information regarding a prior resource from which
	// the publication was derived.
	Source []MetadataValue `json:",omitempty"`
	// Subject identifies the subject of the EPUB Publication.
	Subject []MetadataValue `json:",omitempty"`
	// Type is used to indicate that the given EPUB Publication is of a
	// specialized type.
	Type []MetadataValue `json:",omitempty"`

	// Meta element provides a generic means of including package
	// metadata.
	Meta []GenericMetadata `json:",omitempty"`

	// Series is the series to which this book belongs to.
	Series MetadataValue `json:",omitempty"`
	// SeriesIndex is the position in the series to which the book belongs to.
	SeriesIndex MetadataValue `json:",omitempty"`
}

// MarshalJSON omits empty series values while retaining the established JSON shape.
func (m Information) MarshalJSON() ([]byte, error) {
	type information Information
	var series, index *MetadataValue
	if !m.Series.omitted() {
		series = &m.Series
	}
	if !m.SeriesIndex.omitted() {
		index = &m.SeriesIndex
	}
	return json.Marshal(struct {
		information
		Series      *MetadataValue `json:",omitempty"`
		SeriesIndex *MetadataValue `json:",omitempty"`
	}{information(m), series, index})
}

// Identifier represents an identifier.
type Identifier struct {
	Scheme      string
	Value       string
	Refinements []Meta     `json:",omitempty"`
	Attributes  []xml.Attr `json:",omitempty"`
}

// Author represents an author.
type Author struct {
	FullName string
	FileAs   string
	Role     string
	// Refinements contains properties targeting this author.
	// Explicit refinements override Role and FileAs properties of the same name.
	Refinements []Meta     `json:",omitempty"`
	Attributes  []xml.Attr `json:",omitempty"`
}

// Date represents an event.
type Date struct {
	Stamp       string
	Event       string
	Refinements []Meta     `json:",omitempty"`
	Attributes  []xml.Attr `json:",omitempty"`
}

// GenericMetadata represents a generic metadata.
type GenericMetadata struct {
	Name        string
	Content     string
	Refinements []Meta     `json:",omitempty"`
	Attributes  []xml.Attr `json:",omitempty"`
}

func (m *Information) refinementGroups() [][]Meta {
	groups := [][]Meta{m.Series.Refinements, m.SeriesIndex.Refinements}
	for _, values := range [][]MetadataValue{
		m.Title, m.SubTitle, m.Language, m.Coverage, m.Description, m.Format,
		m.Publisher, m.Relation, m.Rights, m.Source, m.Subject, m.Type,
	} {
		for _, v := range values {
			groups = append(groups, v.Refinements)
		}
	}
	for _, v := range m.Identifier {
		groups = append(groups, v.Refinements)
	}
	for _, values := range [][]Author{m.Creator, m.Contributor} {
		for _, v := range values {
			groups = append(groups, v.Refinements)
		}
	}
	for _, v := range m.Date {
		groups = append(groups, v.Refinements)
	}
	for _, v := range m.Meta {
		groups = append(groups, v.Refinements)
	}
	return groups
}

// GetMetadataFromFile reads metadata from an epub file.
func GetMetadataFromFile(path string) (*Information, error) {
	e, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer e.Close()

	return e.Information()
}

func getMeta(mdata *Metadata) *Information {
	m := &Information{
		Language:    metadataValues(mdata.Language, mdata.Meta),
		Subject:     metadataValues(mdata.Subject, mdata.Meta),
		Description: metadataValues(mdata.Description, mdata.Meta),
		Publisher:   metadataValues(mdata.Publisher, mdata.Meta),
		Type:        metadataValues(mdata.Type, mdata.Meta),
		Format:      metadataValues(mdata.Format, mdata.Meta),
		Source:      metadataValues(mdata.Source, mdata.Meta),
		Relation:    metadataValues(mdata.Relation, mdata.Meta),
		Coverage:    metadataValues(mdata.Coverage, mdata.Meta),
		Rights:      metadataValues(mdata.Rights, mdata.Meta),
	}

	m.Title, m.SubTitle = getTitles(mdata.Title, mdata.Meta)

	for _, id := range mdata.Identifier {
		m.Identifier = append(m.Identifier, Identifier{
			Value:       id.Value,
			Scheme:      id.Scheme,
			Refinements: refinementsFor(id.ID, mdata.Meta),
			Attributes:  id.Attributes,
		})
	}

	for _, auth := range mdata.Creator {
		m.Creator = append(m.Creator, getAuth(auth, mdata.Meta))
	}

	for _, auth := range mdata.Contributor {
		m.Contributor = append(m.Contributor, getAuth(auth, mdata.Meta))
	}

	for _, date := range mdata.Date {
		m.Date = append(m.Date, Date{
			Stamp:       date.Value,
			Event:       date.Event,
			Refinements: refinementsFor(date.ID, mdata.Meta),
			Attributes:  date.Attributes,
		})
	}

	m.Series, m.SeriesIndex = seriesValues(mdata.Meta)

	for _, meta := range mdata.Meta {
		if meta.Name != "" && meta.Content != "" {
			refinements := refinementsFor(meta.ID, mdata.Meta)
			if isSeries(meta.Name) {
				refinements = nil
			}
			m.Meta = append(m.Meta, GenericMetadata{
				Name:        meta.Name,
				Content:     meta.Content,
				Refinements: refinements,
				Attributes:  meta.Attributes,
			})
		}
	}

	return m
}

func seriesValues(meta []MetaLegacy) (series, index MetadataValue) {
	if collection := seriesCollection(meta); collection != nil {
		series = NewMetadataValue(collection.Value)
		series.Refinements = refinementsFor(collection.ID, meta, "collection-type", "group-position")
		series.Attributes = collection.Attributes
		positionFound := false
		for _, m := range meta {
			if m.Meta != nil && m.Refines == "#"+collection.ID && m.Property == "group-position" {
				if !positionFound && m.Value != "" {
					index.Value = &m.Meta.Value
				}
				positionFound = true
				index.Refinements = refinementsFor(m.ID, meta)
				index.Attributes = m.Attributes
			}
		}
		return series, index
	}
	for _, m := range meta {
		var target *MetadataValue
		switch m.Name {
		case "calibre:series":
			target = &series
		case "calibre:series_index":
			target = &index
		default:
			continue
		}
		*target = MetadataValue{Refinements: refinementsFor(m.ID, meta), Attributes: m.Attributes}
		if m.Content != "" {
			target.Value = NewMetadataValue(m.Content).Value
		}
	}
	return series, index
}

func metadataValues(elt []Element, meta []MetaLegacy) []MetadataValue {
	s := make([]MetadataValue, len(elt))

	for i, e := range elt {
		s[i] = MetadataValue{Value: &elt[i].Value, Refinements: refinementsFor(e.ID, meta), Attributes: e.Attributes}
	}
	return s
}

func refinementsFor(id string, meta []MetaLegacy, excluded ...string) []Meta {
	var values []Meta
	if id == "" {
		return nil
	}
	for _, m := range meta {
		if m.Meta == nil || m.Refines != "#"+id || m.Property == "" {
			continue
		}
		if !slices.Contains(excluded, m.Property) {
			values = append(values, *m.Meta)
		}
	}
	return values
}

func getAuth(auth AuthorElt, meta []MetaLegacy) Author {
	a := Author{
		FullName:   auth.Value,
		Role:       auth.Role,
		FileAs:     auth.FileAs,
		Attributes: auth.Attributes,
	}

	for _, m := range refinementsFor(auth.ID, meta) {
		switch m.Property {
		case "role":
			a.Role = m.Value
		case "file-as":
			a.FileAs = m.Value
		default:
			a.Refinements = append(a.Refinements, m)
		}
	}

	return a
}

func getTitles(elt []Element, meta []MetaLegacy) (title []MetadataValue, subtitle []MetadataValue) {
	for i, e := range elt {
		value := MetadataValue{Value: &elt[i].Value, Refinements: refinementsFor(e.ID, meta), Attributes: e.Attributes}
		isSubtitle := false
		for _, m := range value.Refinements {
			if m.Property == "title-type" && m.Value == "subtitle" {
				isSubtitle = true
				break
			}
		}
		if isSubtitle {
			value.Refinements = refinementsFor(e.ID, meta, "title-type")
			subtitle = append(subtitle, value)
		} else {
			title = append(title, value)
		}
	}
	return title, subtitle
}

func seriesCollection(meta []MetaLegacy) *Meta {
	for _, m := range meta {
		if m.Meta == nil || m.Property != "belongs-to-collection" || m.Value == "" {
			continue
		}
		isSeries := true
		for _, refinement := range meta {
			if refinement.Meta != nil && refinement.Refines == "#"+m.ID &&
				refinement.Property == "collection-type" && refinement.Value != "series" {
				isSeries = false
				break
			}
		}
		if isSeries {
			return m.Meta
		}
	}
	return nil
}
