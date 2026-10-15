package epub

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
)

const legacyRefinementName = "epub:refinement"

func legacyRefinementElement(start xml.StartElement, value xml.CharData) (xml.StartElement, error) {
	m := Meta{Value: string(value)}
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "id":
			m.ID = a.Value
		case "property":
			m.Property = a.Value
		case "refines":
			m.Refines = a.Value
		case "scheme":
			m.Scheme = a.Value
		case "dir":
			m.Dir = a.Value
		case "lang":
			m.Lang = a.Value
		}
	}
	if !validXMLText(m.Value) {
		return xml.StartElement{}, fmt.Errorf("invalid XML text in metadata refinement")
	}
	content, err := json.Marshal(m)
	if err != nil {
		return xml.StartElement{}, err
	}
	if _, err := decodeLegacyRefinement(string(content)); err != nil {
		return xml.StartElement{}, err
	}
	result := xml.StartElement{Name: start.Name, Attr: []xml.Attr{
		attr("name", legacyRefinementName), attr("content", string(content)),
	}}
	if m.ID != "" {
		result.Attr = append(result.Attr, attr("id", m.ID))
	}
	return result, nil
}

func decodeLegacyRefinement(content string) (*Meta, error) {
	var m Meta
	if err := json.Unmarshal([]byte(content), &m); err != nil {
		return nil, fmt.Errorf("invalid %s JSON: %w", legacyRefinementName, err)
	}
	if strings.TrimSpace(m.Property) == "" || !strings.HasPrefix(m.Refines, "#") || len(m.Refines) < 2 {
		return nil, fmt.Errorf("invalid %s: property and local Refines target are required", legacyRefinementName)
	}
	for _, value := range []string{m.ID, m.Property, m.Refines, m.Value, m.Scheme, m.Dir, m.Lang} {
		if !validXMLText(value) {
			return nil, fmt.Errorf("invalid XML text in %s", legacyRefinementName)
		}
	}
	return &m, nil
}

// Decode only the reserved fallback entries; ordinary legacy metadata is unchanged.
func expandLegacyRefinements(metadata *Metadata) (*Metadata, error) {
	result := *metadata
	result.Meta = make([]MetaLegacy, 0, len(metadata.Meta))
	for _, entry := range metadata.Meta {
		if entry.Name == legacyRefinementName {
			m, err := decodeLegacyRefinement(entry.Content)
			if err != nil {
				return nil, err
			}
			entry = MetaLegacy{Meta: m}
		}
		result.Meta = append(result.Meta, entry)
	}
	return &result, nil
}
