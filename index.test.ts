import assert from "node:assert/strict";
import test from "node:test";

import {
  analyzeIdnHostname,
  hostnameForDisplay,
  shouldDisplayUnicode,
  toAsciiHostname,
  toUnicodeHostname,
} from "./index.ts";

test("converts Unicode labels to A-labels before deciding", () => {
  assert.equal(toAsciiHostname("münchen.de"), "xn--mnchen-3ya.de");
  assert.equal(toUnicodeHostname("xn--mnchen-3ya.de"), "münchen.de");
});

test("rejects a PayPal-style Latin and Cyrillic mix and displays Punycode", () => {
  for (const hostname of ["раураl.com", "раураӏ.com"]) {
    const result = analyzeIdnHostname(hostname);

    assert.equal(result.rejected, true, hostname);
    assert.equal(
      result.reasons.includes("mixed-script") ||
        result.reasons.includes("cyrillic-confusable"),
      true,
      hostname,
    );
    assert.equal(result.display, result.ascii);
    assert.match(result.display, /^xn--/);
    assert.equal(hostnameForDisplay(hostname), result.ascii);
  }
});

test("rejects an all-Cyrillic label whose characters all have Latin lookalikes", () => {
  const result = analyzeIdnHostname("аррӏе.com");

  assert.equal(result.rejected, true);
  assert.equal(result.primaryReason, "cyrillic-confusable");
  assert.equal(result.ascii, "xn--80ak6aa92e.com");
  assert.equal(result.display, "xn--80ak6aa92e.com");
  assert.equal(shouldDisplayUnicode("аррӏе.com"), false);
});

test("allows Japanese kanji and kana, including a mixed Han and kana label", () => {
  for (const hostname of ["日本語.jp", "こんにちは.jp", "日本語ひらがな.jp"]) {
    const result = analyzeIdnHostname(hostname);
    assert.equal(result.rejected, false, hostname);
    assert.equal(result.unicode, hostname);
    assert.equal(result.display, hostname);
    assert.equal(shouldDisplayUnicode(hostname), true);
  }
});

test("allows a legal German Latin-script IDN", () => {
  const result = analyzeIdnHostname("münchen.de");

  assert.equal(result.rejected, false);
  assert.equal(result.unicode, "münchen.de");
  assert.equal(result.ascii, "xn--mnchen-3ya.de");
  assert.equal(result.display, "münchen.de");
});

test("rejects leading combining marks", () => {
  const result = analyzeIdnHostname("\u0301example.com");

  assert.equal(result.rejected, true);
  assert.equal(result.reasons.includes("leading-combining-mark"), true);
  assert.equal(result.display, "xn--example-sge.com");
});

test("rejects invisible formatting characters", () => {
  for (const hostname of [
    "apple\u200b.com",
    "apple\u2060.com",
    "apple\ufeff.com",
    "\ufeffapple.com",
    "apple.com\ufeff",
  ]) {
    const result = analyzeIdnHostname(hostname);
    assert.equal(result.rejected, true, hostname);
    assert.equal(result.reasons.includes("invisible-character"), true, hostname);
  }
});

test("applies the same policy to an A-label input", () => {
  const result = analyzeIdnHostname("xn--80ak6aa92e.com");

  assert.equal(result.rejected, true);
  assert.equal(result.primaryReason, "cyrillic-confusable");
  assert.equal(result.display, "xn--80ak6aa92e.com");
});

test("shows a safe A-label in Unicode after canonical decoding", () => {
  const result = analyzeIdnHostname("xn--mnchen-3ya.de");

  assert.equal(result.rejected, false);
  assert.equal(result.ascii, "xn--mnchen-3ya.de");
  assert.equal(result.unicode, "münchen.de");
  assert.equal(result.display, "münchen.de");
});

test("rejects Latin mixed with Greek", () => {
  const result = analyzeIdnHostname("gοοgle.com");

  assert.equal(result.rejected, true);
  assert.equal(result.primaryReason, "mixed-script");
  assert.equal(result.display, result.ascii);
  assert.match(result.display, /^xn--/);
});

test("does not reject a single-script Cyrillic label with a non-confusable character", () => {
  const result = analyzeIdnHostname("пример.рф");

  assert.equal(result.rejected, false);
  assert.equal(result.display, "пример.рф");
});

test("does not flag ordinary ASCII or mixed Latin and CJK labels", () => {
  for (const hostname of ["apple.com", "apple日本語.jp", "example123.test"]) {
    const result = analyzeIdnHostname(hostname);
    assert.equal(result.rejected, false, hostname);
    assert.equal(result.display, hostname);
  }
});

test("fails closed for malformed or non-hostname input", () => {
  for (const hostname of [
    "",
    "https://example.com/path",
    "example.com:443",
    "xn--.com",
  ]) {
    const result = analyzeIdnHostname(hostname);
    assert.equal(result.rejected, true, hostname);
    assert.equal(result.display, "", hostname);
    assert.equal(result.reasons.includes("invalid-hostname"), true, hostname);
  }
});
