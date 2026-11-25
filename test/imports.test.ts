import assert from "node:assert/strict";
import { test } from "node:test";
import * as packageRoot from "../src/index.ts";
import * as testingEntry from "../src/testing/index.ts";

test("public entry points expose the documented runtime API", () => {
  for (const name of [
    "AuthorizationService",
    "InMemoryCasStore",
    "FaultInjectingCasStore",
    "InMemoryPolicyStore",
    "InMemoryBusinessGateway",
    "signGrant",
    "verifyGrant",
    "canonicalizeForm",
    "topoSort",
    "SubmitStatus",
    "SubmitCode",
  ]) {
    assert.ok(name in packageRoot, `missing export: ${name}`);
  }
  for (const name of ["ScriptedCasStore", "DeterministicRng", "runAsAgent", "buildGrant", "buildSubmit"]) {
    assert.ok(name in testingEntry, `missing testing export: ${name}`);
  }
});
