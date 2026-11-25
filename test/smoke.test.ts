import assert from "node:assert/strict";
import { test } from "node:test";
import * as publicApi from "../src/index.ts";
import { ReferenceAuthorizationEngine } from "../src/reference-engine.ts";
import { IncrementalAuthorizationEngine } from "../src/incremental-engine.ts";

test("public entry points are importable", () => {
  assert.equal(typeof ReferenceAuthorizationEngine, "function");
  assert.equal(typeof IncrementalAuthorizationEngine, "function");
  for (const name of [
    "canonicalJSON",
    "generateKeyPair",
    "sealStatement",
    "buildSenderReceipts",
    "verifyBasisSignatures",
    "subtract",
    "purposeCovers",
  ]) {
    assert.equal(
      name in publicApi,
      true,
      `${name} should be exported from the package entry`,
    );
  }
});
