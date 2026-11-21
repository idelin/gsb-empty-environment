import test from "node:test";
import assert from "node:assert/strict";

import {
  assertNoLeak,
  containsLeak,
  findLeaks,
} from "../src/index.ts";

const PHONE = "13800138000";
const EMAIL = "alice@example.com";

test("detects raw, encoded, and double-encoded leaks", () => {
  const base64 = Buffer.from(PHONE).toString("base64");
  const hex = Buffer.from(PHONE).toString("hex");
  const unicode = [...PHONE]
    .map(
      (character) =>
        `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`,
    )
    .join("");
  const html = [...PHONE]
    .map((character) => `&#x${character.codePointAt(0)!.toString(16)};`)
    .join("");
  const doubleEncoded = encodeURIComponent(encodeURIComponent(EMAIL));
  const spacedBase64 = base64.replace(/(.{4})/g, "$1 ").trim();
  const spacedHex = hex.replace(/(.{2})/g, "$1 ").trim();

  const cases = [
    `raw:${PHONE}`,
    `base64:${base64}`,
    `base64url:${Buffer.from(PHONE).toString("base64url")}`,
    `hex:${hex}`,
    `spacedBase64:${spacedBase64}`,
    `spacedHex:${spacedHex}`,
    `unicode:${unicode}`,
    `html:${html}`,
    `separated:138-0013-8000`,
    `encodedEmail:${encodeURIComponent(EMAIL)}`,
    `doubleEncodedEmail:${doubleEncoded}`,
    `mixedUnicodeEmail:alice\\u0040example.com`,
    `mixedHtmlEmail:alice&#64;example.com`,
    `mixedNamedHtmlEmail:alice&commat;example.com`,
  ];

  for (const text of cases) {
    assert.equal(
      containsLeak(text, [PHONE, EMAIL]),
      true,
      `expected a leak in ${text}`,
    );
  }
});

test("detects values split across array elements and object fields", () => {
  assert.equal(
    containsLeak(["138", "0013", "8000"], [PHONE]),
    true,
  );
  assert.equal(
    containsLeak({ a: "138", b: "0013", c: "8000" }, [PHONE]),
    true,
  );

  const base64 = Buffer.from(PHONE).toString("base64");
  assert.equal(
    containsLeak(
      { chunks: [base64.slice(0, 6), base64.slice(6, 12), base64.slice(12)] },
      [PHONE],
    ),
    true,
  );
  assert.equal(
    containsLeak(["138", "0013", "8000"], [PHONE], {
      splitAcrossFields: true,
    }),
    true,
  );
});

test("detects compacted forms when the sensitive value contains separators", () => {
  assert.equal(
    containsLeak("prefix CUSTOMER42XYZ suffix", ["CUSTOMER-42-XYZ"]),
    true,
  );
  assert.equal(
    containsLeak("mail alice@examplecom", [EMAIL]),
    true,
  );
});

test("reports the encoding used by a leak", () => {
  const base64 = Buffer.from(PHONE).toString("base64");
  const leaks = findLeaks(`value=${base64}`, [PHONE]);
  assert.ok(leaks.length > 0);
  assert.equal(leaks[0].value, PHONE);
  assert.match(leaks[0].encoding, /base64/);
});

test("honours minimum-length matching for short values", () => {
  assert.equal(containsLeak("abcdef", ["abc"]), false);
  assert.equal(
    containsLeak("abcdef", ["abc"], { minLength: 1 }),
    true,
  );
  assert.equal(containsLeak("abc", ["abc"]), true);
});

test("supports reversed, case-insensitive, and NFKC detection options", () => {
  const reversed = [...PHONE].reverse().join("");
  assert.equal(
    containsLeak(reversed, [PHONE], { reversed: true }),
    true,
  );

  assert.equal(
    containsLeak("SUPERSECRET42", ["SuperSecret42"], {
      caseInsensitive: true,
    }),
    true,
  );

  const fullWidth = [...PHONE]
    .map((digit) => String.fromCharCode(digit.charCodeAt(0) + 0xfee0))
    .join("");
  assert.equal(
    containsLeak(fullWidth, [PHONE], { normalized: true }),
    true,
  );
});

test("assertNoLeak is a usable verification primitive", () => {
  assert.throws(() => assertNoLeak(`raw ${PHONE}`, [PHONE]));
  assert.doesNotThrow(() => assertNoLeak("nothing here", [PHONE]));
});

test("split detection can be disabled explicitly", () => {
  assert.equal(
    containsLeak(["138", "0013", "8000"], [PHONE], {
      splitAcrossFields: false,
    }),
    false,
  );
});
