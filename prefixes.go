package epub

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/beevik/etree"
)

var (
	vocabularyPrefix = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._-]*$`)
)

func validatePrefix(prefix, iri string) error {
	if !vocabularyPrefix.MatchString(prefix) || strings.EqualFold(prefix, "xml") || prefix == "xmlns" {
		return fmt.Errorf("invalid vocabulary prefix %q", prefix)
	}
	u, err := url.Parse(iri)
	if err != nil || !u.IsAbs() || !validXMLText(iri) || strings.ContainsAny(iri, " \t\r\n") {
		return fmt.Errorf("prefix %q requires an absolute IRI: %q", prefix, iri)
	}
	return nil
}

func parsePrefixes(value string) (map[string]string, error) {
	fields := strings.Fields(value)
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("invalid package prefix declaration %q", value)
	}
	var prefixes map[string]string
	for i := 0; i < len(fields); i += 2 {
		if !strings.HasSuffix(fields[i], ":") {
			return nil, fmt.Errorf("invalid package prefix declaration %q", value)
		}
		prefix, iri := strings.TrimSuffix(fields[i], ":"), fields[i+1]
		if err := validatePrefix(prefix, iri); err != nil {
			return nil, err
		}
		if previous, exists := prefixes[prefix]; exists && previous != iri {
			return nil, fmt.Errorf("conflicting declarations for prefix %q", prefix)
		}
		if prefixes == nil {
			prefixes = make(map[string]string)
		}
		prefixes[prefix] = iri
	}
	return prefixes, nil
}

func updatePackagePrefixes(root *etree.Element, additions map[string]string) (bool, error) {
	if len(additions) == 0 {
		return false, nil
	}
	if !strings.HasPrefix(root.SelectAttrValue("version", ""), "3") {
		return false, fmt.Errorf("package prefix declarations require EPUB 3; custom EPUB 2 attributes do not need them")
	}
	value := root.SelectAttrValue("prefix", "")
	existing, err := parsePrefixes(value)
	if err != nil {
		return false, err
	}
	var added []string
	for prefix, iri := range additions {
		if err := validatePrefix(prefix, iri); err != nil {
			return false, err
		}
		if previous, exists := existing[prefix]; exists {
			if previous != iri {
				return false, fmt.Errorf("prefix %q is already declared as %q, not %q", prefix, previous, iri)
			}
			continue
		}
		added = append(added, prefix+": "+iri)
	}
	if len(added) == 0 {
		return false, nil
	}
	sort.Strings(added)
	value = strings.TrimSpace(value)
	if value != "" {
		value += " "
	}
	value += strings.Join(added, " ")
	root.CreateAttr("prefix", value)
	return true, nil
}
