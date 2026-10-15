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
    Title: []epub.MetadataValue{epub.NewMetadataValue("A New Title")},
    Creator: []epub.Author{
        {FullName: "Jane Doe", FileAs: "Doe, Jane", Role: "aut"},
    },
})
if err != nil {
    panic(err)
}
```

Text fields use `MetadataValue`,
whose `Value *string` distinguishes omitted text from explicitly empty text.
A nil `Value` preserves the existing text.
Omitted fields and JSON `null` leave values unchanged.
An object with an omitted or null `Value` can update only refinements or
attributes without repeating the text.
In text lists, entries match existing values by position.
Omitted property lists preserve those properties.
An entry without `Value` requires an existing
value at that position; otherwise the update fails without changing the file.
List updates still replace the entire list, so include entries for every value
you want to retain. An explicit empty string writes empty text.
Title and subtitle updates are independent. Supplying replacement text also
replaces its associated refinements and attributes.
EPUB 3 updates refresh the `dcterms:modified` timestamp.
The primary identifier cannot be removed, since the package references it.

#### Field refinements

Every metadata value supports directly attached refinements using the
existing `Meta` type. Callers choose the property names; the library does not
define application-specific properties.

Text fields (`Title`, `SubTitle`, `Language`, `Subject`, etc.) now use
`[]MetadataValue` instead of `[]string`, with an optional `Value *string`.
Identifiers, authors, dates, and legacy name/content metadata
retain their existing types and each has a `Refinements []Meta` field.
This is a Go API change: use `.String()` to read text safely, or inspect `.Value`
to distinguish omitted text from an explicit empty string.
Construct text updates with `NewMetadataValue`,
or use `MetadataValue{Refinements: ...}` / `MetadataValue{Attributes: ...}` for
property-only updates without repeating the text.

```go
title := "A New Title"
err := epub.WriteMetadataToFile("book.epub", &epub.Information{
    Prefixes: map[string]string{
        "myvocab": "https://example.org/vocabulary/",
        "cdr": "https://example.org/cdr/",
    },
    Title: []epub.MetadataValue{{
        Value: &title,
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

Refinements require EPUB 3. The writer sets `refines` automatically, overriding
`Meta.Refines`. Properties must be nonempty; explicit properties override generated
ones such as `role`. Optional `ID`, `Scheme`, `Lang`, and `Dir` are supported;
IDs must be unique.

`Prefixes` maps vocabulary prefixes to absolute IRIs and merges them into the
package's `prefix` attribute. Existing declarations are preserved; conflicting
mappings are rejected. Reading metadata returns these declarations and refinements,
with standard author and subtitle properties exposed through convenience fields.

The CLI accepts plain strings or refined objects for text values,
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

Use `Attributes []xml.Attr` (from `encoding/xml`) on any metadata value:

```go
err := epub.WriteMetadataToFile("book.epub", &epub.Information{
    Creator: []epub.Author{{
        FullName: "Horacio",
        Role: "aut",
        Attributes: []xml.Attr{
            {Name: xml.Name{Local: "wd_id"}, Value: "Q6197"},
        },
    }},
})
```

This writes `wd_id="Q6197"` directly on the creator. Equivalent CLI JSON:

```json
{
  "Creator": [{
    "FullName": "Horacio",
    "Role": "aut",
    "Attributes": [{"Name": {"Local": "wd_id"}, "Value": "Q6197"}]
  }]
}
```

For namespaced attributes, use the URI in `Name.Space` and the local name in
`Name.Local`. Choose a prefix with `Namespaces`:

```go
const vocab = "https://github.com/svera/coreander/vocab/"
err := epub.WriteMetadataToFile("book.epub", &epub.Information{
    Namespaces: map[string]string{"cdr": vocab},
    Creator: []epub.Author{{
        FullName: "Isaac Asimov",
        Role: "aut",
        FileAs: "Asimov, Isaac",
        Attributes: []xml.Attr{
            {Name: xml.Name{Space: vocab, Local: "wd_id"}, Value: "Q34981"},
        },
    }},
})
```

The writer declares `cdr` once on `<metadata>` and reuses inherited namespaces.
Without a preference, it reuses an existing prefix or generates one.
`Namespaces` is a write-only option for either EPUB version, separate from EPUB 3
vocabulary `Prefixes`. Prefixes must be valid, nonreserved names with absolute
URIs; conflicting bindings are rejected.

Attributes round-trip on read. Use existing fields for managed attributes such
as `id` and `opf:role`. Custom attributes may not pass EPUB validation or be
interpreted by other readers. Old `epub:refinement` wrappers are ordinary `Meta`
entries; exclude them when writing `Meta` to remove them.

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

# Read updates from stdin; omitted fields stay unchanged
printf '%s' '{"Title":["New Title"]}' | epub --write - mybook.epub
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
  "Publisher": ["Example Publisher"]
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
