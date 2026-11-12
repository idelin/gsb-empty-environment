import assert from "node:assert/strict";
import { test } from "node:test";
import {
  ArtifactVerifier,
  createCacheSignatureHandler,
  isValidTaskHash,
  signArtifact,
} from "../src/cache-signature.ts";
import {
  HASH_A,
  HangingRequest,
  KEY_A,
  MemoryBackend,
  TEAM_A,
  chunked,
  encode,
  makeRequest,
} from "./helpers.ts";

const HANGING_LABEL = "not-hex";

test("task hash must be exactly 16 lowercase hex characters", () => {
  assert.equal(isValidTaskHash(HASH_A), true);
  for (const invalid of [
    "0123456789abcde",
    "0123456789abcdef0",
    "0123456789ABCDEF",
    "0123456789abcdeg",
    "0123456-89abcdef",
    "",
  ]) {
    assert.equal(isValidTaskHash(invalid), false, invalid);
  }
});

test("missing, wrong-length, uppercase, and non-hex labels are rejected", async () => {
  const backend = new MemoryBackend();
  const secrets = new Map([[TEAM_A, KEY_A]]);
  const handler = createCacheSignatureHandler({ backend, secrets });
  const artifact = encode("build-output");

  const cases: Array<[string | null, number]> = [
    [null, 400],
    ["", 400],
    ["0".repeat(63), 400],
    ["0".repeat(65), 400],
    ["A".repeat(64), 400],
    [`${"0".repeat(63)}z`, 400],
  ];
  for (const [label, expectedStatus] of cases) {
    const response = await handler(makeRequest("PUT", TEAM_A, HASH_A, label, artifact));
    assert.equal(response.status, expectedStatus, `label ${JSON.stringify(label)}`);
    assert.equal(backend.storeCalls.length, 0);
  }
});

test("streamed uploads verify across HMAC block and byte boundaries", async () => {
  const artifact = new Uint8Array(400);
  for (let index = 0; index < artifact.length; index += 1) {
    artifact[index] = (index * 13 + 5) & 0xff;
  }
  const signature = signArtifact(KEY_A, TEAM_A, HASH_A, artifact);

  for (const chunkSize of [1, 2, 15, 16, 55, 63, 64, 65, 128, 255, 400]) {
    const backend = new MemoryBackend();
    const secrets = new Map([[TEAM_A, KEY_A]]);
    const handler = createCacheSignatureHandler({ backend, secrets });

    const response = await handler(
      makeRequest("PUT", TEAM_A, HASH_A, signature, chunked(artifact, chunkSize)),
    );
    assert.equal(response.status, 204, `chunk size ${chunkSize}`);
    assert.deepEqual(backend.loadArtifact(TEAM_A, HASH_A), artifact);

    const download = await handler(makeRequest("GET", TEAM_A, HASH_A, signature));
    assert.equal(download.status, 200);
    assert.deepEqual(new Uint8Array(await download.arrayBuffer()), artifact);
  }
});

test("store callback never receives bytes before verification passes", async () => {
  const backend = new MemoryBackend();
  const secrets = new Map([[TEAM_A, KEY_A]]);
  const handler = createCacheSignatureHandler({ backend, secrets });
  const artifact = encode("secret-build-output");

  const response = await handler(
    makeRequest("PUT", TEAM_A, HASH_A, "f".repeat(64), chunked(artifact, 4)),
  );
  assert.equal(response.status, 401);
  assert.deepEqual(backend.storeCalls, []);
});

test("malformed labels are rejected before the request body is read", async () => {
  const backend = new MemoryBackend();
  const secrets = new Map([[TEAM_A, KEY_A]]);
  const handler = createCacheSignatureHandler({ backend, secrets });

  const response = await handler(new HangingRequest(TEAM_A, HASH_A, HANGING_LABEL));
  assert.equal(response.status, 400);
  assert.equal(backend.storeCalls.length, 0);
});

test("verifier never exposes buffered bytes on failure", () => {
  const verifier = new ArtifactVerifier(KEY_A, TEAM_A, HASH_A);
  verifier.update(encode("secret"));
  const result = verifier.finish("f".repeat(64));
  assert.equal(result.ok, false);
  assert.equal(result.bytes.length, 0);
});
