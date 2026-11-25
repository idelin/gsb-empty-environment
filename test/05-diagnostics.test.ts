import { test } from "node:test";
import assert from "node:assert/strict";
import {
  createDefaultVerifier,
  SimulatedCryptoBackend,
  Verifier,
} from "../src/index.ts";
import {
  buildBundle,
  buildRecords,
  LAYOUT,
  PACKAGE_ID,
} from "../src/fixtures/builder.ts";

const validBase = {
  algorithm: "ed25519-v1",
  keyMaterialId: "key-vendor",
  domain: "domain-vendor",
  packageId: PACKAGE_ID,
  payloadSchema: "schema-supplier-v2",
  canonicalVersion: "2.0",
  scope: "scope-supplier",
  coveredRange: { start: LAYOUT.lenSupplier.start, end: LAYOUT.supplier.end },
  declaredAttributes: ["vendor.identity"],
};

test("unknown algorithm, unbound schema and ambiguous map are reported with distinct codes", () => {
  const records = buildRecords([
    { ...validBase, index: 0, algorithm: "rsa-pss-unknown" },
    { ...validBase, index: 1, algorithm: "ed25519-v1", payloadSchema: "schema-ghost" },
    { ...validBase, index: 2, algorithm: "ed25519-v1" },
  ]);
  const bundle = buildBundle(records, { ambiguousSupplierMap: true });
  const report = createDefaultVerifier().evaluate(bundle);

  const codes = report.diagnostics.map((diagnostic) => diagnostic.code);
  assert.ok(codes.includes("UNKNOWN_ALGORITHM"));
  assert.ok(codes.includes("UNBOUND_SCHEMA"));
  assert.ok(codes.includes("AMBIGUOUS_REVERSE_MAP"));

  // Every defective record is excluded before any crypto call.
  assert.equal(report.verified.length, 0);
  assert.equal(report.totalVerificationCalls, 0);

  const unknown = report.diagnostics.find((entry) => entry.code === "UNKNOWN_ALGORITHM");
  const unbound = report.diagnostics.find((entry) => entry.code === "UNBOUND_SCHEMA");
  const ambiguous = report.diagnostics.find((entry) => entry.code === "AMBIGUOUS_REVERSE_MAP");
  assert.equal(unknown?.recordIndex, 0);
  assert.equal(unbound?.recordIndex, 1);
  assert.equal(ambiguous?.canonicalId, "canon-supplier");
});

test("package identity mismatch and scope mismatch are binding failures, not crypto calls", () => {
  const records = buildRecords([
    { ...validBase, index: 0, packageId: "pkg-someone-else" },
    { ...validBase, index: 1, scope: "scope-build" },
  ]);
  const bundle = buildBundle(records);
  const report = createDefaultVerifier().evaluate(bundle);

  const codes = report.diagnostics.map((diagnostic) => diagnostic.code);
  assert.deepEqual(codes.sort(), ["CANONICAL_SCOPE_MISMATCH", "PACKAGE_ID_MISMATCH"]);
  assert.equal(report.totalVerificationCalls, 0);
});

test("cryptographic failure is reported once and the record never witnesses", () => {
  const records = buildRecords([{ ...validBase, index: 0, validSignature: false }]);
  const bundle = buildBundle(records);
  const report = createDefaultVerifier().evaluate(bundle);

  assert.equal(report.totalVerificationCalls, 1);
  assert.deepEqual(report.diagnostics.map((entry) => entry.code), ["CRYPTO_FAILURE"]);
  assert.equal(report.verified.length, 0);
  const witness = report.witnesses.find((entry) => entry.attribute === "vendor.identity");
  assert.equal(witness?.satisfied, false);
});

test("each structurally valid record reaches the backend exactly once regardless of how many attributes it declares", () => {
  let calls = 0;
  const backend = new SimulatedCryptoBackend();
  const original = backend.verify.bind(backend);
  backend.verify = (record, bundle) => {
    calls += 1;
    return original(record, bundle);
  };
  const records = buildRecords([
    { ...validBase, index: 0, declaredAttributes: ["vendor.identity", "build.provenance"] },
  ]);
  const bundle = buildBundle(records);
  new Verifier(backend).evaluate(bundle);
  assert.equal(calls, 1);
});
