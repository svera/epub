package epub

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MetadataValue is a text value with optional refinements. The writer
// sets each refinement's Refines target automatically.
// EPUB 2 uses JSON legacy meta entries; EPUB 3 uses native refinement elements.
// In JSON, plain values are strings; refined values are objects.
type MetadataValue struct {
	Value       string
	Refinements []Meta
}

func (v MetadataValue) String() string { return v.Value }

// MarshalJSON preserves the string representation of unrefined values.
func (v MetadataValue) MarshalJSON() ([]byte, error) {
	if v.Refinements == nil {
		return json.Marshal(v.Value)
	}
	type value MetadataValue
	return json.Marshal(value(v))
}

// UnmarshalJSON accepts a string or a value with attached refinements.
func (v *MetadataValue) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*v = MetadataValue{}
		return nil
	}
	if len(bytes.TrimSpace(data)) != 0 && bytes.TrimSpace(data)[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		*v = MetadataValue{Value: text}
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
	if m.Series.Value != "" || m.Series.Refinements != nil {
		series = &m.Series
	}
	if m.SeriesIndex.Value != "" || m.SeriesIndex.Refinements != nil {
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
	Refinements []Meta `json:",omitempty"`
}

// Author represents an author.
type Author struct {
	FullName string
	FileAs   string
	Role     string
	// Refinements contains properties targeting this author.
	// Explicit refinements override Role and FileAs properties of the same name.
	Refinements []Meta `json:",omitempty"`
}

// Date represents an event.
type Date struct {
	Stamp       string
	Event       string
	Refinements []Meta `json:",omitempty"`
}

// GenericMetadata represents a generic metadata.
type GenericMetadata struct {
	Name        string
	Content     string
	Refinements []Meta `json:",omitempty"`
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
			})
		}
	}

	return m
}

func seriesValues(meta []MetaLegacy) (series, index MetadataValue) {
	series.Value, index.Value = getSeries(meta)
	if collection := seriesCollection(meta); collection != nil {
		series.Refinements = refinementsFor(collection.ID, meta, "collection-type", "group-position")
		for _, m := range meta {
			if m.Meta != nil && m.Refines == "#"+collection.ID && m.Property == "group-position" {
				index.Refinements = refinementsFor(m.ID, meta)
				break
			}
		}
	} else {
		for _, m := range meta {
			switch m.Name {
			case "calibre:series":
				series.Refinements = refinementsFor(m.ID, meta)
			case "calibre:series_index":
				index.Refinements = refinementsFor(m.ID, meta)
			}
		}
	}
	return series, index
}

func metadataValues(elt []Element, meta []MetaLegacy) []MetadataValue {
	s := make([]MetadataValue, len(elt))

	for i, e := range elt {
		s[i] = MetadataValue{Value: e.Value, Refinements: refinementsFor(e.ID, meta)}
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
		skip := false
		for _, property := range excluded {
			skip = skip || m.Property == property
		}
		if !skip {
			values = append(values, *m.Meta)
		}
	}
	return values
}

func getAuth(auth AuthorElt, meta []MetaLegacy) Author {
	a := Author{
		FullName: auth.Value,
		Role:     auth.Role,
		FileAs:   auth.FileAs,
	}

	for _, m := range meta {
		if m.Meta == nil || auth.ID == "" || m.Refines != "#"+auth.ID {
			continue
		}

		switch m.Property {
		case "role":
			a.Role = m.Value
		case "file-as":
			a.FileAs = m.Value
		default:
			if m.Property != "" {
				a.Refinements = append(a.Refinements, *m.Meta)
			}
		}
	}

	return a
}

// getTitles extracts titles and subtitles from elements and metadata.
// It prioritizes elements with title-type metadata to identify subtitles.
func getTitles(elt []Element, meta []MetaLegacy) (title []MetadataValue, subtitle []MetadataValue) {
	// Build a map of element ID to its metadata for O(1) lookup
	metaMap := make(map[string][]MetaLegacy)
	for _, m := range meta {
		if m.Refines != "" {
			metaMap[m.Refines] = append(metaMap[m.Refines], m)
		}
	}

	for _, e := range elt {
		// Check if this element has title-type metadata
		if metas, exists := metaMap["#"+e.ID]; exists {
			for _, m := range metas {
				if m.Property == "title-type" {
					switch m.Value {
					case "subtitle":
						subtitle = append(subtitle, MetadataValue{Value: e.Value, Refinements: refinementsFor(e.ID, meta, "title-type")})
						goto nextElt
					}
				}
			}
		}
		title = append(title, MetadataValue{Value: e.Value, Refinements: refinementsFor(e.ID, meta)})
	nextElt:
	}

	return title, subtitle
}

// getSeries extracts series information from Meta.
// It supports both Calibre-like EPUB 2 series coding and EPUB3 collection metadata.
// If both are available, EPUB3 is preferred.
func getSeries(meta []MetaLegacy) (series string, seriesIndex string) {
	// First, look for EPUB3 collection metadata (priority)
	if m := seriesCollection(meta); m != nil {
		series = m.Value
		for _, mm := range meta {
			if mm.Meta != nil && mm.Refines == "#"+m.ID && mm.Property == "group-position" {
				seriesIndex = mm.Value
				break
			}
		}
		return series, seriesIndex
	}

	// Fall back to Calibre-style metadata
	for _, m := range meta {
		switch m.Name {
		case "calibre:series":
			series = m.Content
		case "calibre:series_index":
			seriesIndex = m.Content
		}
	}

	return series, seriesIndex
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
