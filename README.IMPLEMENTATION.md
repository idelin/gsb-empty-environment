# Safe HTML/SVG/MathML Sanitizer

Zero-dependency TypeScript implementation of a defensive HTML5 fragment
parser, whitelist sanitizer, canonical serializer and re-parse idempotency
verifier. It runs on Node.js's native TypeScript type stripping (`node
--experimental-strip-types`) and requires no third-party packages.

## Why a parser-based sanitizer

Regular-expression filtering of `<script>` / `on*` cannot reason about the
browser's tree-construction state machine. Attack payloads such as

```html
<math><mtext><table><mglyph><style><!--</style>
  <img src=x onerror=alert(1)>
```

exploit HTML5 foreign-content (MathML/SVG) namespace transitions, table
foster parenting and raw-text comment handling so that the tree a browser
builds on the *second* parse differs from the tree that was serialized. This
is the classic mutation XSS (mXSS) class.

The library therefore implements:

1. A full tokenizer state machine (data, RCDATA, RAWTEXT, PLAINTEXT, script
   data with escape/double-escape states, comments, CDATA, DOCTYPE, quoted
   and unquoted attributes, character references).
2. A tree builder with the HTML/SVG/MathML namespace state machine,
   including MathML text integration points (`mtext`, `mi`, `mo`, `mn`,
   `ms`), HTML integration points (`annotation-xml[encoding=text/html|
   application/xhtml+xml]`, SVG `foreignObject`, `desc`, `title`), the
   MathML/SVG break-out tag sets, SVG/MathML tag and attribute name
   adjustments, and table foster parenting.
3. A whitelist sanitizer that hard-strips `script`, `style`, `iframe`,
   `object`, `embed`, `base`, `meta`, `noscript` and friends, removes every
   `on*` handler, and validates URI attributes against protocol obfuscation
   (whitespace, control characters, single and double entity encoding).
4. A structural auditor that rejects dangerous foreign-content nesting and
   comments that disguise tag boundaries.
5. A canonical serializer plus a mandatory parse → serialize → re-parse
   closed loop; any tag, namespace, attribute or text drift is reported as an
   `idempotency-drift` violation with source location.

## Usage

```ts
import { sanitize, SanitizationError } from './src/index.ts';

// Accepted input returns stable HTML plus informational violations.
const result = sanitize('<p onclick="x">hi</p>');
result.html;       // '<p>hi</p>'
result.stable;     // true
result.violations; // [{ kind: 'banned-attribute', ... }]

// Hard failures throw and carry the mXSS payload location.
try {
  sanitize('<math><mtext><table><mglyph><style><!--</style>' +
    '<img src=x onerror=alert(1)>');
} catch (error) {
  if (error instanceof SanitizationError) {
    error.violations[0].kind;     // 'dangerous-nesting'
    error.violations[0].location; // { offset, line, column }
    error.violations[0].path;     // element ancestry
  }
}
```

### Options

- `rejectReplacementChar` (default `true`): reject U+FFFD produced from null
  bytes or invalid character references.
- `allowStyleAttribute` (default `false`): enable `style` with a conservative
  CSS guard that blocks `url()`, `@import`, `expression()`, comments and
  dangerous schemes.
- `keepComments` (default `false`): retain well-formed comments; comments that
  contain tag spoofing are always rejected.
- `allowedProtocols`: extra URL schemes added to the default allow-list
  (`http:`, `https:`, `mailto:`, `tel:`, `ftp:`, `ftps:`).

## Violation kinds

| Kind | Severity | Meaning |
| --- | --- | --- |
| `banned-element` | stripped | Non-whitelisted/HARD-STRIPPED tag removed or unwrapped |
| `banned-attribute` | stripped | `on*`, non-whitelisted or disabled attribute removed |
| `banned-protocol` | stripped | URI/srcset uses a blocked or obfuscated scheme |
| `dangerous-nesting` | reject | Foreign-content/table mode jump or non-whitelisted foreign tag |
| `dangerous-comment` | reject | Comment hides tag/comment boundary markup |
| `replacement-character` | reject | U+FFFD in text or attribute value |
| `idempotency-drift` | reject | Re-parsed tree differs from the sanitized AST |

## Module layout

- `src/tokenizer.ts` — HTML5 tokenizer state machine.
- `src/tree-builder.ts` — fragment tree builder, namespaces, integration
  points, foster parenting and structural event recording.
- `src/policy.ts` — tag/attribute/URI whitelists.
- `src/url-safety.ts` — scheme normalization, double entity decoding, CSS
  guard.
- `src/serializer.ts` — canonical HTML5 fragment serializer.
- `src/idempotency.ts` — closed-loop re-parse comparison.
- `src/sanitizer.ts` — orchestration and structural auditing.
- `src/entities.ts` — named/numeric character reference tables.
- `test/*.test.ts` — adversarial suite (mXSS vectors, protocol matrix,
  namespace rules, idempotency).

## Running tests

```sh
npm test
```
