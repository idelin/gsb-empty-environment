import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildCanonicalSpec,
  createDefaultVerifier,
  enumerateSubsets,
  SimulatedCryptoBackend,
  signToken,
  validateCanonicalMap,
  Verifier,
} from "../src/index.ts";

test("modules load and canonical builder produces an ordered reverse map", () => {
  const raw = new TextEncoder().encode("a\r\nb");
  const built = buildCanonicalSpec(
    "c1",
    { start: 0, end: raw.length },
    raw,
    { version: "1.0", foldNewlines: true, stripPadding: false },
  );
  assert.deepEqual(Array.from(built.canonicalBytes), Array.from(new TextEncoder().encode("a\nb")));
  assert.equal(validateCanonicalMap(built.spec).ok, true);
  const verifier = createDefaultVerifier();
  assert.ok(verifier instanceof Verifier);
  assert.ok(new SimulatedCryptoBackend().knownAlgorithms.size > 0);
  assert.ok(signToken({ index: 0, algorithm: "ed25519-v1", keyMaterialId: "k" }).length > 0);
  assert.equal(Array.from(enumerateSubsets([1, 2])).length, 3);
});
