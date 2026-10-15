package epub

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"sort"
)

const xmlNamespace = "http://www.w3.org/XML/1998/namespace"

type xmlNamespaces struct {
	bindings  map[string]string
	preferred map[string]string
	added     map[string]string
}

func newXMLNamespaces(inherited, preferred map[string]string) (*xmlNamespaces, error) {
	n := &xmlNamespaces{
		bindings: make(map[string]string), preferred: make(map[string]string),
		added: make(map[string]string),
	}
	for prefix, uri := range inherited {
		n.bindings[prefix] = uri
	}
	n.bindings["xml"] = xmlNamespace
	for _, prefix := range sortedNamespacePrefixes(preferred) {
		uri := preferred[prefix]
		if err := validatePrefix(prefix, uri); err != nil {
			return nil, fmt.Errorf("XML namespace: %w", err)
		}
		if uri == xmlNamespace || uri == "http://www.w3.org/2000/xmlns/" {
			return nil, fmt.Errorf("reserved XML namespace URI %q", uri)
		}
		if previous, exists := n.bindings[prefix]; exists && previous != uri {
			return nil, fmt.Errorf("XML namespace prefix %q is already bound to %q, not %q", prefix, previous, uri)
		}
		if _, exists := n.bindings[prefix]; !exists {
			n.bindings[prefix], n.added[prefix] = uri, uri
		}
		if _, exists := n.preferred[uri]; !exists {
			n.preferred[uri] = prefix
		}
	}
	return n, nil
}

func sortedNamespacePrefixes(bindings map[string]string) []string {
	prefixes := make([]string, 0, len(bindings))
	for prefix := range bindings {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	return prefixes
}

func (n *xmlNamespaces) name(name xml.Name, attribute bool) xml.Name {
	if name.Space == "" {
		return name
	}
	prefix, found := n.preferred[name.Space]
	if !found {
		for _, candidate := range sortedNamespacePrefixes(n.bindings) {
			if n.bindings[candidate] == name.Space && (!attribute || candidate != "") {
				prefix, found = candidate, true
				break
			}
		}
	}
	if !found {
		switch name.Space {
		case dcNamespace:
			prefix = "dc"
		case opfNamespace:
			prefix = "opf"
		default:
			prefix = "ns"
		}
		base := prefix
		for i := 1; ; i++ {
			if _, exists := n.bindings[prefix]; !exists {
				break
			}
			prefix = fmt.Sprintf("%s%d", base, i)
		}
		n.bindings[prefix], n.added[prefix] = name.Space, name.Space
	}
	if prefix != "" {
		name.Local = prefix + ":" + name.Local
	}
	return xml.Name{Local: name.Local}
}

func (n *xmlNamespaces) declare(data []byte, source *packageXML, generatedLength int) ([]byte, error) {
	var declarations bytes.Buffer
	for _, prefix := range sortedNamespacePrefixes(n.added) {
		declarations.WriteString(` xmlns:` + prefix + `="`)
		if err := xml.EscapeText(&declarations, []byte(n.added[prefix])); err != nil {
			return nil, err
		}
		declarations.WriteByte('"')
	}
	end := source.metadataStartEnd
	selfClosing := data[end-2] == '/'
	if selfClosing && generatedLength != 0 {
		// Generated children follow the original self-closing metadata tag.
		return bytes.Join([][]byte{
			data[:end-2], declarations.Bytes(), []byte(">"),
			data[end : end+generatedLength], []byte("</" + source.metadataName + ">"), data[end+generatedLength:],
		}, nil), nil
	}
	if declarations.Len() == 0 {
		return data, nil
	}
	insert := end - 1
	if selfClosing {
		insert--
	}
	return bytes.Join([][]byte{data[:insert], declarations.Bytes(), data[insert:]}, nil), nil
}
