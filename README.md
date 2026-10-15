# EPUB

[![GoDoc](https://pkg.go.dev/badge/github.com/pirmd/epub)](https://pkg.go.dev/github.com/pirmd/epub)
[![Go Report Card](https://goreportcard.com/badge/github.com/pirmd/epub)](https://goreportcard.com/report/github.com/pirmd/epub)
[![License](https://img.shields.io/badge/license-BSD-blue.svg)](LICENSE)

`epub` is a Go package for reading and updating metadata in EPUB files. It provides comprehensive support for both EPUB 2 and EPUB 3.2 specifications.

## Features

- **Metadata Extraction**: Read titles, authors, identifiers (ISBN, DOI, UUID), languages, dates, publishers, and more
- **Package Document Parsing**: Full support for EPUB OPF (Open Packaging Format) files
- **Resource Access**: Walk through EPUB files, publication resources, and reading content
- **EPUB 2 & 3.2 Compatible**: Works with both legacy and modern EPUB formats
- **Series Support**: Extract series information from Calibre and EPUB3 metadata
- **Metadata Writing**: Apply partial updates without rewriting unrelated package XML or publication resources

## Installation

```bash
go get github.com/pirmd/epub
```

## Usage

### Basic Metadata Extraction

```go
package main

import (
    "fmt"
    "github.com/pirmd/epub"
)

func main() {
    // Read metadata from an EPUB file
    metadata, err := epub.GetMetadataFromFile("book.epub")
    if err != nil {
        panic(err)
    }

    // Access metadata
    fmt.Printf("Title: %v\n", metadata.Title)
    fmt.Printf("Authors: %v\n", metadata.Creator)
    fmt.Printf("Language: %v\n", metadata.Language)
    fmt.Printf("Publisher: %v\n", metadata.Publisher)
    
    // Access identifiers (ISBN, DOI, etc.)
    for _, id := range metadata.Identifier {
        fmt.Printf("Identifier (%s): %s\n", id.Scheme, id.Value)
    }
}
```

### Writing Metadata

Use `WriteMetadataToFile` with the existing `Information` type to change metadata in place:

```go
err := epub.WriteMetadataToFile("book.epub", &epub.Information{
    Title: []epub.MetadataValue{{Value: "A New Title"}},
    Creator: []epub.Author{
        {FullName: "Jane Doe", FileAs: "Doe, Jane", Role: "aut"},
    },
    Subject: []epub.MetadataValue{}, // Clear subjects.
    Series: epub.MetadataValue{Value: "My Series"},
})
if err != nil {
    panic(err)
}
```

When marshaling updates to JSON, `Information`'s `omitempty` tags omit empty
optional lists; include an explicit `[]` in JSON to clear those fields.

Nil slices and empty series values without refinements preserve existing fields.
Empty, non-nil slices clear fields. To clear series values, pass their names explicitly:

```go
err := epub.WriteMetadataToFile("book.epub", &epub.Information{}, "Series")
```

The clear list accepts `Series` and `SeriesIndex` and takes precedence over
supplied values. Clearing `Series` also clears its index; clearing only
`SeriesIndex` preserves the series name. Unknown clear fields are rejected.
Title and subtitle updates are independent. Replacing a field also replaces its
associated refinements. `Meta` replaces legacy name/content metadata except
Calibre series entries; use `Series` and `SeriesIndex` to update those. Series
updates use Calibre metadata for EPUB 2 and collection metadata for EPUB 3.
EPUB 3 updates refresh the `dcterms:modified` timestamp.
Collections explicitly typed as something other than a series are preserved
and are not reported as series metadata.
The primary identifier cannot be removed, since the package references it.

#### Field refinements

Every metadata value supports directly attached refinements using the
existing `Meta` type. Callers choose the property names; the library does not
define application-specific properties.

Text fields (`Title`, `SubTitle`, `Language`, `Subject`, etc.) now use
`[]MetadataValue` instead of `[]string`. `Series` and `SeriesIndex` also use
`MetadataValue`. Identifiers, authors, dates, and legacy name/content metadata
retain their existing types and each has a `Refinements []Meta` field.
This is a Go API change: use `.Value` to access text and series values.

```go
err := epub.WriteMetadataToFile("book.epub", &epub.Information{
    Prefixes: map[string]string{
        "myvocab": "https://example.org/vocabulary/",
        "cdr": "https://example.org/cdr/",
    },
    Title: []epub.MetadataValue{{
        Value: "A New Title",
        Refinements: []epub.Meta{{
            Property: "myvocab:catalogue_id",
            Value: "book-123",
        }},
    }},
    Creator: []epub.Author{{
        FullName: "Jane Doe",
        Role: "aut",
        Refinements: []epub.Meta{{
            Property: "cdr:wd_id",
            Value: "Q42",
        }},
    }},
})
```

The writer automatically sets `refines` to the corresponding field value's XML ID,
reusing existing IDs by position or generating IDs for new values.
Any supplied `Meta.Refines` is overridden. Declare custom vocabulary prefixes
through `Information.Prefixes`, a map from prefix names (without `:`) to absolute
IRIs. The writer merges them into the EPUB 3 package's `prefix` attribute,
preserving unrelated declarations. Repeating an identical mapping is a no-op;
conflicting mappings, invalid names, and relative IRIs are rejected before the
file is replaced. Omitted or empty maps leave existing declarations unchanged.
Reading metadata returns the package's explicit declarations in `Prefixes`.
Prefix updates require EPUB 3.
Any nonempty property name is accepted, including standard properties.
Explicit refinements override automatically generated properties of the same
name (for example, `role` overrides `Author.Role`).
Empty properties are rejected. Explicit refinements require EPUB 3; for custom
EPUB 2 attributes, use `Attributes` as described below.
Optional `Meta` attributes (`ID`, `Scheme`, `Lang`, and `Dir`) are supported;
IDs must be unique in the package.

Reading metadata returns attached refinements. Standard author role/file-as,
subtitle classification, and series position are still interpreted by the
corresponding convenience fields.
Updating a value replaces its refinements; include the refinements you want to
keep. Omitting the entire field list preserves its existing metadata.
For series, refinements can be updated without repeating the value; a nil
refinement list preserves an omitted series value's properties. An explicit
empty refinement list clears its refinements while keeping the series value.
`Series.Refinements` targets the collection; `SeriesIndex.Refinements` targets
its position element. Calibre series entries in `Meta` remain controlled by
`Series` and `SeriesIndex`.

The CLI accepts plain strings or refined objects for text and series values,
including a mixture in the same list:

```json
{
  "Prefixes": {
    "myvocab": "https://example.org/vocabulary/",
    "cdr": "https://example.org/cdr/"
  },
  "Title": [
    {"Value":"A New Title","Refinements":[{"Property":"myvocab:catalogue_id","Value":"book-123"}]},
    "An Alternative Title"
  ],
  "Creator": [
    {"FullName":"Jane Doe","Refinements":[{"Property":"cdr:wd_id","Value":"Q42"}]}
  ]
}
```

Text values with nil refinements and attributes are still emitted as JSON strings.
Values with explicit refinement or attribute lists (including empty lists) are
emitted as objects.
Existing plain-string JSON update files remain accepted.

#### Custom XML attributes (EPUB 2 and EPUB 3)

Use `Attributes []xml.Attr` to attach custom attributes directly to any metadata
value, including authors, titles, identifiers, dates, legacy meta entries, and
series values. For example, matching a Sigil-style author attribute:

```go
err := epub.WriteMetadataToFile("book.epub", &epub.Information{
    Creator: []epub.Author{{
        FullName: "Horacio",
        Role: "aut",
        Attributes: []xml.Attr{ // import "encoding/xml"
            {Name: xml.Name{Local: "wd_id"}, Value: "Q6197"},
        },
    }},
})
```

This writes the attribute on the creator itself (alongside its managed ID and
role), not in a wrapper:

```xml
<dc:creator wd_id="Q6197" ...>Horacio</dc:creator>
```

The CLI accepts the same attributes in JSON:

```json
{
  "Creator": [{
    "FullName": "Horacio",
    "Role": "aut",
    "Attributes": [{"Name": {"Local": "wd_id"}, "Value": "Q6197"}]
  }]
}
```

Attributes are returned when reading metadata. Updating a value replaces its
attributes; include any you want to keep. Omitting a field preserves it.
For series, an omitted value with nil `Attributes` preserves existing attributes;
an explicit empty list clears them without changing the series value.

For namespaced attributes, set `xml.Name.Space` to the namespace URI and `Local`
to the local name, not a colon-qualified name. Namespace declarations are generated
automatically; `Information.Prefixes` only declares EPUB 3 property vocabularies.
Invalid names, duplicate attributes, and invalid XML characters are rejected.
Do not use custom attributes to override managed fields such as `id` or
`opf:role`; use their existing API fields instead.

Direct custom attributes are not EPUB 3 refinements. Sigil accepting them does
not guarantee EPUB validation or support in other readers.
The writer no longer generates JSON `epub:refinement` entries, nor does the reader
interpret them. Existing entries are left as ordinary `Information.Meta`
values; to remove them, write that list back with those entries excluded
(or use an empty list to clear all non-series legacy entries).

The writer copies other ZIP entries without recompression, preserves package XML
outside the updated metadata, and replaces the original file only after completing
the new archive. File permissions are retained. Symbolic links are rejected.
Reopen any existing `Epub` readers to observe updates. As with any metadata edit,
digital signatures covering the package document will no longer be valid.

### Access Package Document

```go
package main

import (
    "fmt"
    "github.com/pirmd/epub"
)

func main() {
    // Get the full PackageDocument
    pkg, err := epub.GetPackageFromFile("book.epub")
    if err != nil {
        panic(err)
    }

    fmt.Printf("EPUB Version: %s\n", pkg.Version)
    fmt.Printf("Unique Identifier: %s\n", pkg.UniqueIdentifier)
    
    // Access manifest items
    for _, item := range pkg.Manifest.Items {
        fmt.Printf("Item: %s (type: %s)\n", item.ID, item.MediaType)
    }
}
```

### Walking EPUB Resources

```go
package main

import (
    "fmt"
    "io"
    "github.com/pirmd/epub"
)

func main() {
    // Walk all files in the EPUB
    err := epub.WalkFiles("book.epub", func(r io.Reader, info interface{}) error {
        fmt.Printf("File: %s\n", info.Name())
        return nil
    })
    if err != nil {
        panic(err)
    }

    // Walk only publication resources (from manifest)
    err = epub.WalkPublicationResources("book.epub", func(r io.Reader, info interface{}) error {
        fmt.Printf("Publication Resource: %s\n", info.Name())
        return nil
    })

    // Walk reading content (from spine, in order)
    err = epub.WalkReadingContent("book.epub", func(r io.Reader, info interface{}) error {
        fmt.Printf("Reading Content: %s\n", info.Name())
        return nil
    })
}
```

### Using the Epub Type

```go
package main

import (
    "fmt"
    "github.com/pirmd/epub"
)

func main() {
    // Open an EPUB file
    e, err := epub.Open("book.epub")
    if err != nil {
        panic(err)
    }
    defer e.Close()

    // Get package document
    pkg, err := e.Package()
    if err != nil {
        panic(err)
    }

    // Get simplified metadata
    info, err := e.Information()
    if err != nil {
        panic(err)
    }
    fmt.Printf("Title: %v\n", info.Title)

    // Open a specific item by href
    file, err := e.OpenItem("chapter1.xhtml")
    if err != nil {
        panic(err)
    }
    defer file.Close()
    
    // Read the file content
    // ...
}
```

## Command Line Tool

A command-line tool is available to print EPUB metadata in JSON format and apply
JSON metadata updates.

### Installation

```bash
go install github.com/pirmd/epub/cmd/epub@latest
```

### Usage

```bash
# Print all metadata
epub mybook.epub

# Pretty-print JSON output (default)
epub --pretty mybook.epub

# Compact JSON output
epub --compact mybook.epub

# Print only specific fields
epub --field title mybook.epub
epub --field author,language mybook.epub

# Process multiple files
epub file1.epub file2.epub

# Update metadata in place and print the result
epub --write metadata.json mybook.epub

# Read updates from stdin; omitted fields stay unchanged, [] clears a list
printf '%s' '{"Title":["New Title"],"Subject":[]}' | epub --write - --clear Series mybook.epub
```

The JSON update object accepts the same field names as `Information`.
`null` leaves a field unchanged. Unknown fields and invalid JSON are rejected
before any EPUB is modified. One update can be applied to multiple input files;
each file is replaced independently, and a failure results in a nonzero exit code.

### Example Output

```json
{
  "Title": ["The Great Novel"],
  "Creator": [
    {
      "FullName": "Jane Doe",
      "FileAs": "Doe, Jane",
      "Role": "author"
    }
  ],
  "Identifier": [
    {
      "Scheme": "ISBN",
      "Value": "978-1-23456-789-0"
    }
  ],
  "Language": ["en"],
  "Publisher": ["Example Publisher"],
  "Series": "My Series",
  "SeriesIndex": "1"
}
```

## EPUB Specifications

This library aims to support:

- [EPUB 3.2 Specification](https://www.w3.org/publishing/epub32/epub-packages.html)
- EPUB 2 (legacy support)

## Contributing

Contributions are welcome! Please follow these steps:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the BSD License - see the [LICENSE](LICENSE) file for details.

[modeline]: # ( vim: set fenc=utf-8 spell spl=en: )
