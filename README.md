# IDN display policy

All-Cyrillic `аррӏе.com` looks like `apple.com`, so rendering raw Unicode in an
address bar can help an attacker masquerade. This browser-safe TypeScript
package converts a hostname to its canonical A-label first, then decides whether
the Unicode form is safe to display.

## Policy

- A label may contain Latin together with CJK scripts.
- Latin may not be mixed with Cyrillic or Greek. Other mixed-script labels are
  rejected too.
- An all-Cyrillic label is rejected when every character has a known Latin
  lookalike.
- A label beginning with a combining mark is rejected.
- A hostname containing an invisible format/control character is rejected.
- Rejected hostnames display their canonical Punycode (A-label) form.

## API

```ts
import {
  analyzeIdnHostname,
  hostnameForDisplay,
  shouldDisplayUnicode,
  toAsciiHostname,
} from "netnew-20261112-0755";

const result = analyzeIdnHostname("münchen.de");
// {
//   ascii: "xn--mnchen-3ya.de",
//   unicode: "münchen.de",
//   display: "münchen.de",
//   safeForUnicode: true,
//   rejected: false,
//   reasons: [],
//   ...
// }

hostnameForDisplay("аррӏе.com"); // "xn--80ak6aa92e.com"
shouldDisplayUnicode("раураl.com"); // false
toAsciiHostname("日本語.jp"); // "xn--wgv71a119e.jp"
```

`analyzeIdnHostname` never throws for untrusted hostname input. It records
`invalid-hostname` when URL/IDNA canonicalization fails. If no fallback A-label
can be produced, `display` is empty; policy rejections such as a leading
combining mark still receive an RFC 3492 Punycode fallback.

## Tests

```sh
npm test
```
