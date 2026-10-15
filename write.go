package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/beevik/etree"
	"golang.org/x/net/html/charset"
)

const (
	dcNamespace  = "http://purl.org/dc/elements/1.1/"
	opfNamespace = "http://www.idpf.org/2007/opf"
)

var xmlEncoding = regexp.MustCompile(`\bencoding\s*=\s*("[^"]*"|'[^']*')`)

// WriteMetadataToFile applies updates to the default rendition of an EPUB.
// Nil slices leave fields unchanged; non-nil empty slices clear fields.
// A nil MetadataValue.Value preserves existing text and omitted properties.
// Text-list entries match existing values by position; missing targets fail.
// Explicitly empty series values clear the series or index.
// clearFields is a deprecated compatibility option accepting "Series" and
// "SeriesIndex"; it overrides supplied values.
// Meta replaces legacy name/content metadata except Calibre series entries.
// Refinements automatically target each value's XML ID and require EPUB 3.
// Attributes writes custom XML attributes directly in either EPUB version.
// Other XML content and namespace prefixes are preserved, but serialization
// may change quoting, escaping, and empty-element formatting. Other ZIP entries
// are copied unchanged. The original
// file is replaced by a completed temporary archive in the same directory.
// Existing Epub readers must be reopened to see the changes.
// Symbolic links are rejected rather than replaced.
func WriteMetadataToFile(path string, updates *Information, clearFields ...string) (err error) {
	if updates == nil {
		return fmt.Errorf("metadata updates must not be nil")
	}
	clear := make(map[string]bool)
	for _, field := range clearFields {
		switch field {
		case "Series", "SeriesIndex":
			clear[field] = true
		default:
			return fmt.Errorf("unsupported clear field %q: use Series or SeriesIndex; clear list fields with empty slices", field)
		}
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
	data, err = updatePackageMetadata(data, updates, clear)
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
	element    *etree.Element
	name       xml.Name
	id         string
	refines    string
	property   string
	legacyName string
	subtitle   bool
}

type packageEditState struct {
	document   *etree.Document
	metadata   *etree.Element
	children   []metadataChild
	ids        map[string]bool
	namespaces map[string]string
}

func parsePackageXML(data []byte) (*packageEditState, error) {
	doc := etree.NewDocument()
	doc.ReadSettings.Entity = xml.HTMLEntity
	doc.ReadSettings.CharsetReader = charset.NewReaderLabel
	doc.ReadSettings.PreserveCData = true
	doc.ReadSettings.PreserveDuplicateAttrs = true
	if err := doc.ReadFromBytes(data); err != nil {
		return nil, err
	}
	root := doc.Root()
	if root == nil || root.Tag != "package" || root.NamespaceURI() != opfNamespace {
		return nil, fmt.Errorf("invalid package root")
	}
	p := &packageEditState{document: doc, ids: make(map[string]bool), namespaces: make(map[string]string)}
	for _, element := range root.ChildElements() {
		if element.Tag == "metadata" && element.NamespaceURI() == opfNamespace {
			if p.metadata != nil {
				return nil, fmt.Errorf("multiple metadata elements")
			}
			p.metadata = element
		}
	}
	if p.metadata == nil {
		return nil, fmt.Errorf("package has no metadata element")
	}
	for _, element := range []*etree.Element{root, p.metadata} {
		for _, a := range element.Attr {
			if a.Space == "xmlns" {
				p.namespaces[a.Key] = a.Value
			} else if a.Space == "" && a.Key == "xmlns" {
				p.namespaces[""] = a.Value
			}
		}
	}
	var collectIDs func(*etree.Element) error
	collectIDs = func(element *etree.Element) error {
		for _, a := range element.Attr {
			if a.Space == "" && a.Key == "id" {
				if p.ids[a.Value] {
					return fmt.Errorf("duplicate XML ID %q", a.Value)
				}
				p.ids[a.Value] = true
			}
		}
		for _, child := range element.ChildElements() {
			if err := collectIDs(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := collectIDs(root); err != nil {
		return nil, err
	}
	for _, element := range p.metadata.ChildElements() {
		child := metadataChild{
			element: element, name: xml.Name{Space: element.NamespaceURI(), Local: element.Tag},
			id: element.SelectAttrValue("id", ""), refines: element.SelectAttrValue("refines", ""),
			property: element.SelectAttrValue("property", ""), legacyName: element.SelectAttrValue("name", ""),
		}
		p.children = append(p.children, child)
	}
	return p, nil
}

type metadataElement struct {
	name       xml.Name
	value      string
	attributes []xml.Attr
}

func buildMetadata(elements []metadataElement, namespaces *xmlNamespaces) ([]*etree.Element, error) {
	var result []*etree.Element
	for _, value := range elements {
		if !validXMLText(value.value) {
			return nil, fmt.Errorf("invalid XML text in metadata")
		}
		element := etree.NewElement(namespaces.name(value.name, false))
		seen := make(map[xml.Name]bool)
		for _, a := range value.attributes {
			if !vocabularyPrefix.MatchString(a.Name.Local) || a.Name.Local == "xmlns" ||
				a.Name.Space == "xmlns" || a.Name.Space == "http://www.w3.org/2000/xmlns/" {
				return nil, fmt.Errorf("invalid metadata attribute name %q", a.Name.Local)
			}
			if seen[a.Name] {
				return nil, fmt.Errorf("duplicate metadata attribute %q", a.Name.Local)
			}
			seen[a.Name] = true
			if !validXMLText(a.Value) || !validXMLText(a.Name.Space) {
				return nil, fmt.Errorf("invalid XML text in %s attribute %s", value.name.Local, a.Name.Local)
			}
			element.CreateAttr(namespaces.name(a.Name, true), a.Value)
		}
		element.SetText(value.value)
		result = append(result, element)
	}
	return result, nil
}

func updatePackageMetadata(data []byte, u *Information, clear map[string]bool) ([]byte, error) {
	pkg, err := newPackageDocument(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	source, err := parsePackageXML(data)
	if err != nil {
		return nil, err
	}
	prefixesChanged, err := updatePackagePrefixes(source.document.Root(), u.Prefixes)
	if err != nil {
		return nil, err
	}
	namespaces, err := newXMLNamespaces(source.namespaces, u.Namespaces)
	if err != nil {
		return nil, err
	}
	children := source.children
	markSubtitles(children, pkg.Metadata.Meta)
	editor := metadataEditor{
		source: source, removed: make([]bool, len(children)),
		epub3:                strings.HasPrefix(pkg.Version, "3"),
		preservedRefinements: make(map[string]bool),
	}
	if err := editor.prepareRefinements(u.refinementGroups()); err != nil {
		return nil, err
	}
	if err := editor.updateTextFields(u, getMeta(pkg.Metadata)); err != nil {
		return nil, err
	}
	editor.updateIdentifiers(u.Identifier, pkg.UniqueIdentifier)
	editor.updateAuthors("creator", u.Creator)
	editor.updateAuthors("contributor", u.Contributor)
	editor.updateDates(u.Date)
	if err := editor.updateLegacyMetadata(u.Meta); err != nil {
		return nil, err
	}
	if err := editor.updateSeries(u, clear, pkg.Metadata.Meta); err != nil {
		return nil, err
	}
	editor.refreshModifiedTime(prefixesChanged || len(namespaces.added) != 0)
	editor.removeRefinements()
	updated, err := editor.apply(namespaces)
	if err != nil {
		return nil, err
	}
	if _, err := parsePackageXML(updated); err != nil {
		return nil, fmt.Errorf("invalid updated package: %w", err)
	}
	p, err := newPackageDocument(bytes.NewReader(updated))
	if err != nil {
		return nil, fmt.Errorf("invalid updated package: %w", err)
	}
	if u.Identifier != nil && p.UniqueIdentifier != "" {
		for _, id := range p.Metadata.Identifier {
			if id.ID == p.UniqueIdentifier {
				return updated, nil
			}
		}
		return nil, fmt.Errorf("cannot remove the package's primary identifier")
	}
	return updated, nil
}

func markSubtitles(children []metadataChild, meta []MetaLegacy) {
	subtitles := make(map[string]bool)
	for _, m := range meta {
		if m.Meta != nil && m.Property == "title-type" && m.Value == "subtitle" {
			subtitles[m.Refines] = true
		}
	}
	for i := range children {
		c := &children[i]
		if c.name == (xml.Name{Space: dcNamespace, Local: "title"}) && c.id != "" {
			c.subtitle = subtitles["#"+c.id]
		}
	}
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
