import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildCanonicalSpec,
  identityMap,
  validateCanonicalMap,
} from "../src/index.ts";
import { buildRawBytes, LAYOUT } from "../src/fixtures/builder.ts";

test("identity and generated maps validate cleanly", () => {
  const raw = buildRawBytes();
  const built = buildCanonicalSpec(
    "c",
    { ...LAYOUT.supplier },
    raw,
    { version: "2.0", foldNewlines: true, stripPadding: true },
  );
  assert.equal(validateCanonicalMap(built.spec).ok, true);
  assert.equal(
    validateCanonicalMap({
      version: "1",
      foldNewlines: false,
      stripPadding: false,
      rawRange: { start: 0, end: 4 },
      reverseMap: identityMap({ rawRange: { start: 0, end: 4 } }),
    }).ok,
    true,
  );
});

test("two canonical positions pointing at one raw interval are ambiguous", () => {
  const raw = buildRawBytes();
  const built = buildCanonicalSpec(
    "c",
    { ...LAYOUT.build },
    raw,
    { version: "2.0", foldNewlines: false, stripPadding: true, forceAmbiguous: true },
  );
  const result = validateCanonicalMap(built.spec);
  assert.equal(result.ok, false);
  if (!result.ok) assert.ok(result.reason.length > 0);
});

test("one canonical interval mapping to two distinct raw intervals is ambiguous", () => {
  const result = validateCanonicalMap({
    version: "1",
    foldNewlines: false,
    stripPadding: false,
    rawRange: { start: 0, end: 10 },
    reverseMap: [
      { start: 0, end: 2, canonicalStart: 0, canonicalEnd: 2 },
      { start: 5, end: 7, canonicalStart: 0, canonicalEnd: 2 },
    ],
  });
  assert.equal(result.ok, false);
  if (!result.ok) assert.ok(result.reason.length > 0);
});

test("overlapping and out-of-range canonical intervals are rejected", () => {
  const overlapping = validateCanonicalMap({
    version: "1",
    foldNewlines: false,
    stripPadding: false,
    rawRange: { start: 0, end: 10 },
    reverseMap: [
      { start: 0, end: 3, canonicalStart: 0, canonicalEnd: 3 },
      { start: 4, end: 7, canonicalStart: 2, canonicalEnd: 5 },
    ],
  });
  assert.equal(overlapping.ok, false);

  const escaping = validateCanonicalMap({
    version: "1",
    foldNewlines: false,
    stripPadding: false,
    rawRange: { start: 0, end: 4 },
    reverseMap: [{ start: 2, end: 6, canonicalStart: 0, canonicalEnd: 4 }],
  });
  assert.equal(escaping.ok, false);
});
