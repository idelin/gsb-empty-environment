import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildReportData,
  canonicalize,
  decodeReportData,
  extractWrappingKey,
  hashRuntimeData,
  verifyRuntimeDataBinding,
} from "../index.ts";
import { RuntimeDataBindingError } from "../src/errors.ts";
import type { JsonObject, RuntimeData } from "../src/types.ts";

const runtimeData: RuntimeData = {
  "public": "PEM-PLACEHOLDER",
  usage: "wrap",
  nested: { z: 1, a: 2 },
};

test("canonicalization is key-order independent", () => {
  assert.equal(
    canonicalize({ b: 1, a: [2, { d: 4, c: 3 }] }),
    canonicalize({ a: [2, { c: 3, d: 4 }], b: 1 }),
  );
  assert.equal(canonicalize(runtimeData).includes(" "), false);
});

test("hash matches the report_data built from identical runtime-data", () => {
  const reportData = buildReportData(runtimeData, "SHA-384");
  const claims: JsonObject = {
    "runtime-data": { nested: { a: 2, z: 1 }, usage: "wrap", public: "PEM-PLACEHOLDER" },
    report_data: reportData,
  };
  const binding = verifyRuntimeDataBinding(claims, {
    runtimeDataHash: "SHA-384",
  });
  assert.equal(binding.hashAlgorithm, "SHA-384");
  assert.equal(binding.reportData.length, 48);
});

test("swapped wrapping public key breaks the REPORT_DATA binding", () => {
  const reportData = buildReportData(runtimeData, "SHA-384");
  const attackerRuntimeData: RuntimeData = {
    ...runtimeData,
    public: "ATTACKER-PUBLIC-KEY",
  };
  // The host replaces the public key inside runtime-data but the quote still
  // carries the original report_data hash.
  const claims: JsonObject = {
    "runtime-data": attackerRuntimeData,
    report_data: reportData,
  };
  assert.throws(
    () => verifyRuntimeDataBinding(claims, { runtimeDataHash: "SHA-384" }),
    (error: unknown) =>
      error instanceof RuntimeDataBindingError &&
      /hash does not match report_data/.test(error.message),
  );
});

test("tampered report_data is rejected", () => {
  const digest = hashRuntimeData(runtimeData, "SHA-384");
  digest[0] ^= 0xff;
  const claims: JsonObject = {
    "runtime-data": runtimeData,
    report_data: digest.toString("hex"),
  };
  assert.throws(
    () => verifyRuntimeDataBinding(claims, { runtimeDataHash: "SHA-384" }),
    RuntimeDataBindingError,
  );
});

test("missing runtime-data or report_data is rejected", () => {
  assert.throws(
    () => verifyRuntimeDataBinding({}),
    RuntimeDataBindingError,
  );
  assert.throws(
    () =>
      verifyRuntimeDataBinding({
        "runtime-data": runtimeData,
      }),
    /report_data/,
  );
  assert.throws(
    () =>
      verifyRuntimeDataBinding({
        "runtime-data": "not-an-object",
        report_data: buildReportData(runtimeData),
      }),
    /JSON object/,
  );
});

test("report_data accepts hex and base64 and infers the digest length", () => {
  const hex = buildReportData(runtimeData, "SHA-256");
  const base64 = Buffer.from(hex, "hex").toString("base64");
  const claims: JsonObject = {
    "runtime-data": runtimeData,
    "tdx": { "report-data": base64 },
  };
  const binding = verifyRuntimeDataBinding(claims);
  assert.equal(binding.hashAlgorithm, "SHA-256");
  assert.equal(decodeReportData(hex).length, 32);
  assert.throws(() => decodeReportData("not-hex-or-base64!!"), RuntimeDataBindingError);
});

test("wrapping key is extracted only from attested runtime-data", () => {
  assert.equal(
    extractWrappingKey(runtimeData).publicKey,
    "PEM-PLACEHOLDER",
  );
  assert.throws(
    () => extractWrappingKey({ usage: "wrap" }),
    /no wrapping public key/,
  );
  const withAltPath = extractWrappingKey(
    { "wrapping-key": "KEY" },
    ["public", "wrapping-key"],
  );
  assert.equal(withAltPath.path, "wrapping-key");
});
