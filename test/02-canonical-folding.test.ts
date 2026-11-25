import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildCanonicalSpec,
  createDefaultVerifier,
  validateCanonicalMap,
} from "../src/index.ts";
import {
  buildBundle,
  buildRecords,
  buildRawBytes,
  LAYOUT,
  PACKAGE_ID,
} from "../src/fixtures/builder.ts";
import { assertWitnessesMatchOracle } from "./oracle.ts";

test("folded newline: reverse map keeps the CR and LF sides in separate entries", () => {
  const raw = buildRawBytes();
  const { spec, canonicalBytes } = buildCanonicalSpec(
    "canon-supplier",
    { ...LAYOUT.supplier },
    raw,
    { version: "2.0", foldNewlines: true, stripPadding: false },
  );
  assert.equal(validateCanonicalMap(spec).ok, true);
  assert.equal(canonicalBytes.length, LAYOUT.supplier.end - LAYOUT.supplier.start - 1);
  const foldEntry = spec.reverseMap.find(
    (entry) => entry.start === LAYOUT.fold.start && entry.end === LAYOUT.fold.end,
  );
  assert.ok(foldEntry, "the two raw CRLF bytes map to one canonical LF coordinate");
  assert.equal(foldEntry!.canonicalEnd - foldEntry!.canonicalStart, 1);
});

test("folded newline: signing the two sides separately never endorses the whole attribute", () => {
  const records = buildRecords([
    {
      algorithm: "ed25519-v1",
      keyMaterialId: "key-vendor-a",
      domain: "domain-vendor",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-supplier-v2",
      canonicalVersion: "2.0",
      scope: "scope-supplier",
      // Raw bytes before the CRLF plus the length field.
      coveredRange: { start: LAYOUT.lenSupplier.start, end: LAYOUT.fold.start },
      declaredAttributes: ["vendor.identity"],
    },
    {
      algorithm: "ed25519-v1",
      keyMaterialId: "key-vendor-b",
      domain: "domain-vendor",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-supplier-v2",
      canonicalVersion: "2.0",
      scope: "scope-supplier",
      // Raw bytes from the LF side to the end of the region.
      coveredRange: LAYOUT.fold,
      declaredAttributes: ["vendor.identity"],
    },
  ]);
  const bundle = buildBundle(records);
  const report = createDefaultVerifier().evaluate(bundle);

  const witness = report.witnesses.find((entry) => entry.attribute === "vendor.identity");
  assert.equal(witness?.satisfied, false);
  assert.match(witness?.reason ?? "", /no single signature/);
  assertWitnessesMatchOracle(bundle, report);
});

test("padding removal: a signature spanning the full raw region still endorses despite the stripped byte", () => {
  const records = buildRecords([
    {
      algorithm: "ed25519-v1",
      keyMaterialId: "key-builder",
      domain: "domain-build",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-build-v2",
      canonicalVersion: "2.0",
      scope: "scope-build",
      coveredRange: { ...LAYOUT.build },
      declaredAttributes: ["build.provenance"],
    },
  ]);
  const bundle = buildBundle(records);
  const spec = bundle.manifest.canonicalizations["canon-build"];
  assert.equal(spec.stripPadding, true);
  // The padding raw byte has no canonical entry of its own.
  assert.ok(
    spec.reverseMap.every(
      (entry) => !(entry.start <= LAYOUT.padding.start && LAYOUT.padding.end <= entry.end) ||
        entry.end - entry.start > 1,
    ),
  );
  const report = createDefaultVerifier().evaluate(bundle);
  const witness = report.witnesses.find((entry) => entry.attribute === "build.provenance");
  assert.equal(witness?.satisfied, true);
  assert.deepEqual(witness?.recordIndices, [0]);
  assertWitnessesMatchOracle(bundle, report);
});
