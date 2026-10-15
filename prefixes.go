package epub

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var (
	vocabularyPrefix = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._-]*$`)
	xmlAttribute     = regexp.MustCompile(`([^\s=<>/"']+)\s*=\s*("[^"]*"|'[^']*')`)
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

func updatePackagePrefixes(data []byte, pkg *PackageDocument, additions map[string]string) ([]byte, error) {
	if len(additions) == 0 {
		return data, nil
	}
	if !strings.HasPrefix(pkg.Version, "3") {
		return nil, fmt.Errorf("package prefix declarations require EPUB 3; EPUB 2 fallback properties do not need them")
	}
	existing, err := parsePrefixes(pkg.Prefix)
	if err != nil {
		return nil, err
	}
	var added []string
	for prefix, iri := range additions {
		if err := validatePrefix(prefix, iri); err != nil {
			return nil, err
		}
		if previous, exists := existing[prefix]; exists {
			if previous != iri {
				return nil, fmt.Errorf("prefix %q is already declared as %q, not %q", prefix, previous, iri)
			}
			continue
		}
		added = append(added, prefix+": "+iri)
	}
	if len(added) == 0 {
		return data, nil
	}
	sort.Strings(added)
	value := strings.TrimSpace(pkg.Prefix)
	if value != "" {
		value += " "
	}
	value += strings.Join(added, " ")
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(value)); err != nil {
		return nil, err
	}
	// Replace only the package's prefix attribute, preserving all other XML.
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		start := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if _, ok := token.(xml.StartElement); !ok {
			continue
		}
		end := int(decoder.InputOffset())
		for _, match := range xmlAttribute.FindAllSubmatchIndex(data[start:end], -1) {
			if string(data[start+match[2]:start+match[3]]) == "prefix" {
				from, to := start+match[4]+1, start+match[5]-1
				return bytes.Join([][]byte{data[:from], escaped.Bytes(), data[to:]}, nil), nil
			}
		}
		insert := end - 1
		if data[insert-1] == '/' {
			insert--
		}
		attribute := append([]byte(` prefix="`), escaped.Bytes()...)
		attribute = append(attribute, '"')
		return bytes.Join([][]byte{data[:insert], attribute, data[insert:]}, nil), nil
	}
}
