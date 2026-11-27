# PDF Shadow-Attack Structural Verifier

Zero-dependency, raw-byte TypeScript component that verifies the **structural**
integrity of digitally signed PDF documents. It defends against the PDF Shadow
Attack family (Hide / Replace / Hide-and-Replace), where an attacker appends an
incremental update after signing so that the cryptographic signature still
verifies while the viewer renders different content.

Cryptographic PKCS#7/CMS validation is intentionally out of scope. This library
guarantees that the byte ranges handed to a crypto layer are exactly the bytes
that were signed and that no later incremental update can override the signed
object tree.

## What it checks

1. **Full revision chain** - parses every `startxref` -> `xref` table or
   cross-reference stream, following each trailer `/Prev` pointer, recording
   object numbers, generations and absolute byte offsets.
2. **Signature discovery** - locates every `/Sig` and `/DocTimeStamp`
   dictionary, including signatures reached through a field's `/V` reference.
3. **`/ByteRange` coverage** - enforces `off1 === 0`, a non-empty gap, an
   in-bounds suffix, a gap that contains only `/ByteRange [...]` and
   `/Contents <hex>` (no smuggled objects), and coverage through the signing
   revision's `%%EOF` with no uncovered tail garbage.
4. **Default-deny incremental audit** - tracks every object redefined by a
   later xref and walks its reachability graph. Modifications to `/Root`
   (`/Catalog`), `/Pages`, `/Kids`, page `/Contents`, `/Resources`, `/Font` or
   `/Annots` are hard-blocked, as are unreachable/orphan objects and trailer
   root swaps. Only whitelisted, non-visual increments are allowed:
   new signature/timestamp fields and `/DSS` revocation data.

## Usage

```ts
import { verifyPdf } from './src/index.ts';

const report = verifyPdf(pdfBytes); // Uint8Array
if (!report.valid) {
  for (const finding of report.findings) {
    console.error(finding.code, finding.message);
  }
}
// report.signatures[].signedDataRanges / contentsHex feed your crypto layer.
```

Lower-level building blocks are also exported:

- `PdfDocument` / `ByteReader` - byte-level parser and revision chain.
- `findSignatures`, `validateByteRange` - signature location and coverage.
- `auditIncremental`, `buildRoleGraph` - the shadow-attack diff engine.

## Finding codes

| Prefix | Meaning |
| --- | --- |
| `STRUCTURE_PARSE_ERROR`, `HEADER_INVALID`, `NO_SIGNATURE` | Malformed input. |
| `BYTERANGE_*` | `/ByteRange`, gap, snapshot or tail coverage violation. |
| `TAIL_GARBAGE` | Non-whitespace bytes after the last `%%EOF`. |
| `SHADOW_ROOT_SWAP`, `SHADOW_*_MODIFIED`, `SHADOW_OBJECT_FREED` | Protected signed object overridden. |
| `UNAPPROVED_NEW_REACHABLE`, `UNAPPROVED_NEW_OBJECT` | New object outside the signature/DSS whitelist. |
| `SHADOW_NEW_REFERENCE`, `DSS_INVALID_SHAPE` | Illegal reference or shape inside an allowed subtree. |

## Tests

Hand-crafted PDFs (no external PDF tooling) cover a legal timestamp increment,
`/DSS` revocation updates, Hide/Replace/Hide-and-Replace shadow attacks,
annotation and font tampering, orphan objects, tail garbage, cross-reference
streams, and `/ByteRange` hole/truncation/smuggling tampering.

```sh
npm test
```
