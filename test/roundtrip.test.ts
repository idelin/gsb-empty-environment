import assert from "node:assert/strict";
import { test } from "node:test";
import {
  createCacheSignatureHandler,
  signArtifact,
} from "../src/cache-signature.ts";
import {
  HASH_A,
  KEY_A,
  MemoryBackend,
  TEAM_A,
  encode,
  makeRequest,
} from "./helpers.ts";

test("signature label matches Web Crypto HMAC over the NUL-delimited message", async () => {
  const artifact = encode("artifact-payload");
  const message = new Uint8Array([
    ...encode(TEAM_A),
    0x00,
    ...encode(HASH_A),
    0x00,
    ...artifact,
  ]);
  const cryptoKey = await crypto.subtle.importKey(
    "raw",
    KEY_A,
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const platformHex = Buffer.from(
    await crypto.subtle.sign("HMAC", cryptoKey, message),
  ).toString("hex");

  assert.equal(signArtifact(KEY_A, TEAM_A, HASH_A, artifact), platformHex);
  assert.match(platformHex, /^[0-9a-f]{64}$/);
});

test("upload then download roundtrip accepts a valid signature", async () => {
  const backend = new MemoryBackend();
  const secrets = new Map([[TEAM_A, KEY_A]]);
  const handler = createCacheSignatureHandler({ backend, secrets });
  const artifact = encode("build-output-v1");
  const signature = signArtifact(KEY_A, TEAM_A, HASH_A, artifact);

  const putResponse = await handler(makeRequest("PUT", TEAM_A, HASH_A, signature, artifact));
  assert.equal(putResponse.status, 204);
  assert.deepEqual(backend.loadArtifact(TEAM_A, HASH_A), artifact);

  const getResponse = await handler(makeRequest("GET", TEAM_A, HASH_A, signature));
  assert.equal(getResponse.status, 200);
  assert.deepEqual(new Uint8Array(await getResponse.arrayBuffer()), artifact);
});

test("empty artifact upload and download verify correctly", async () => {
  const backend = new MemoryBackend();
  const secrets = new Map([[TEAM_A, KEY_A]]);
  const handler = createCacheSignatureHandler({ backend, secrets });
  const empty = new Uint8Array(0);
  const signature = signArtifact(KEY_A, TEAM_A, HASH_A, empty);

  const putResponse = await handler(makeRequest("PUT", TEAM_A, HASH_A, signature, empty));
  assert.equal(putResponse.status, 204);

  const getResponse = await handler(makeRequest("GET", TEAM_A, HASH_A, signature));
  assert.equal(getResponse.status, 200);
  assert.equal((await getResponse.arrayBuffer()).byteLength, 0);
});

test("download of a missing artifact returns 404", async () => {
  const backend = new MemoryBackend();
  const secrets = new Map([[TEAM_A, KEY_A]]);
  const handler = createCacheSignatureHandler({ backend, secrets });
  const signature = signArtifact(KEY_A, TEAM_A, HASH_A, new Uint8Array(0));

  const response = await handler(makeRequest("GET", TEAM_A, HASH_A, signature));
  assert.equal(response.status, 404);
});
