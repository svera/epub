package epub

import (
	"encoding/xml"
	"fmt"
	"maps"
	"slices"

	"github.com/beevik/etree"
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
	for _, prefix := range slices.Sorted(maps.Keys(preferred)) {
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

func (n *xmlNamespaces) name(name xml.Name, attribute bool) string {
	if name.Space == "" {
		return name.Local
	}
	prefix, found := n.preferred[name.Space]
	if !found {
		for _, candidate := range slices.Sorted(maps.Keys(n.bindings)) {
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
		return prefix + ":" + name.Local
	}
	return name.Local
}

func (n *xmlNamespaces) declare(metadata *etree.Element) {
	for _, prefix := range slices.Sorted(maps.Keys(n.added)) {
		metadata.CreateAttr("xmlns:"+prefix, n.added[prefix])
	}
}
