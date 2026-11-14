# Revision-override assessment for signed PDFs

`netsec` is a self-contained C++20 library for a signing service. It checks
whether a signature byte range covers only an older PDF revision while an
appended incremental update changes what a reader displays.

The component parses classic cross-reference tables, cross-reference streams,
hybrid `/XRefStm` tables, object streams, Flate/ASCIIHex/ASCII85/RunLength
filters, and linearized files with multiple cross-reference sections. Each
object definition, free entry, reused object number, and compressed object is
attributed to one concrete revision.

## API

```cpp
#include "netsec.hpp"

netsec::Assessment result = netsec::assess(
    pdf_bytes,
    std::vector<netsec::ByteRange>{{0, first_revision_end}});

if (!result.valid()) {
    // Signing service rejects the document.
}
```

The primary entry points are:

- `assess(pdf, signed_ranges, policy)`: assess explicit signature byte ranges.
- `assess(pdf, policy)`: read `/ByteRange` values from signature dictionaries.
- `assess_original_revision_only(pdf, policy)`: convenience for a signature
  that claims the original revision.
- `extract_signature_byte_ranges(pdf)`: inspect embedded signature ranges.
- `RevisionOverrideDetector`: stateful wrapper around the same functions.

`Assessment` reports the decision, signed coverage, every revision byte range,
every object version with its revision/type/impact, the visible changes, and a
human-readable difference summary. `Assessment::to_json()` exposes the same
data to a service boundary.

## Policy

The default policy denies every unauthenticated content-changing increment.
The safe exceptions are explicit:

```cpp
// Allow only visible /Text annotations in increments.
auto policy = netsec::Policy::pure_annotations({"Text"});

// Allow a signature widget and its signature field (countersigning).
auto policy = netsec::Policy::countersigning();
```

`Policy` also exposes object-number allowlists, annotation subtype/name
allowlists, and flags for hidden or attachment annotations. Document-level
allowlists are read from `/PermittedChanges` / `/PermittedIncrements`, and
standard `/Perms /DocMDP /P` permissions are honored. Form values, page
content, page order, and embedded files remain denied unless explicitly
allowed by the caller.

An annotation-only update stays valid only when every changed object is an
annotation (or annotation support object), the annotation is allowlisted, and
hidden or attachment annotations are separately permitted. The assessment
still returns the visible difference summary in that case.

## Decision rules

- The original revision is `[0, first %%EOF)`. Each appended revision starts
  at the previous `%%EOF` and ends at its own `%%EOF`.
- A signature that does not cover the complete original revision is rejected.
- Every revision fully covered by the supplied ranges is treated as
  authenticated.
- In an uncovered revision, changes to page text/content, page tree or
  geometry, annotations, form values/defaults, embedded files, document
  structure, encryption, or display resources reject the document.
- Metadata/xref-only updates do not change displayed content and remain valid.
- Object-number reuse, free entries followed by recreation, linearization
  reordering, and compressed objects are reported against the revision that
  introduced the relevant xref entry or object stream.

## Command line

```sh
netsec_cli document.pdf --original-only
netsec_cli document.pdf --range 0:12345 --json
netsec_cli document.pdf --auto
netsec_cli document.pdf --annotations Text --range 0:12345
netsec_cli document.pdf --countersign --range 0:12345
```

Exit status is `0` for a valid assessment, `1` for a rejected document, and `2`
for usage errors.

## Build and test

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

The generated-PDF regression suite covers:

- appended replacement text;
- page replacement/reordering;
- form default value changes;
- hidden and allowlisted visible annotations;
- legitimate countersigning;
- signatures covering every revision;
- object-number reuse, deletion, and recreation;
- compressed object streams and xref streams;
- linearized files with two cross-reference sections;
- embedded attachment changes;
- Flate-compressed page content.

Encrypted documents are rejected rather than guessed at. Unsupported filters
or malformed cross-reference data fail closed with `MalformedDocument`.
