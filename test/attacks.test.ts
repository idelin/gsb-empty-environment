import assert from "node:assert/strict";
import { test } from "node:test";
import {
  SIGNATURE_HEADER,
  createCacheSignatureHandler,
  signArtifact,
} from "../src/cache-signature.ts";
import {
  HASH_A,
  HASH_B,
  KEY_A,
  KEY_B,
  MemoryBackend,
  TEAM_A,
  TEAM_B,
  cacheUrl,
  encode,
  makeRequest,
} from "./helpers.ts";

const secrets = new Map([
  [TEAM_A, KEY_A],
  [TEAM_B, KEY_B],
]);

test("hash swap: a signature for another task hash is rejected", async () => {
  const backend = new MemoryBackend();
  const handler = createCacheSignatureHandler({ backend, secrets });
  const artifact = encode("build-output");
  const signatureForOtherHash = signArtifact(KEY_A, TEAM_A, HASH_B, artifact);

  const response = await handler(
    makeRequest("PUT", TEAM_A, HASH_A, signatureForOtherHash, artifact),
  );
  assert.equal(response.status, 401);
  assert.equal(backend.storeCalls.length, 0);
  assert.equal(backend.loadArtifact(TEAM_A, HASH_A), null);
});

test("cross-team replay: another team's key or label is rejected", async () => {
  const backend = new MemoryBackend();
  const handler = createCacheSignatureHandler({ backend, secrets });
  const artifact = encode("build-output");

  const forgedWithTeamBKey = signArtifact(KEY_B, TEAM_A, HASH_A, artifact);
  const firstResponse = await handler(
    makeRequest("PUT", TEAM_A, HASH_A, forgedWithTeamBKey, artifact),
  );
  assert.equal(firstResponse.status, 401);
  assert.equal(backend.storeCalls.length, 0);

  const replayedFromTeamBPath = signArtifact(KEY_B, TEAM_B, HASH_A, artifact);
  const secondResponse = await handler(
    makeRequest("PUT", TEAM_A, HASH_A, replayedFromTeamBPath, artifact),
  );
  assert.equal(secondResponse.status, 401);
  assert.equal(backend.storeCalls.length, 0);
});

test("unknown team id is rejected without touching the backend", async () => {
  const backend = new MemoryBackend();
  const handler = createCacheSignatureHandler({ backend, secrets });
  const artifact = encode("x");

  const response = await handler(
    makeRequest("PUT", "ghost-team", HASH_A, "0".repeat(64), artifact),
  );
  assert.equal(response.status, 401);
  assert.equal(backend.storeCalls.length, 0);
});

test("post-upload content tampering is rejected on download", async () => {
  const backend = new MemoryBackend();
  const handler = createCacheSignatureHandler({ backend, secrets });
  const artifact = encode("build-output");
  const validSignature = signArtifact(KEY_A, TEAM_A, HASH_A, artifact);

  await handler(makeRequest("PUT", TEAM_A, HASH_A, validSignature, artifact));
  backend.tamper(TEAM_A, HASH_A);

  const response = await handler(makeRequest("GET", TEAM_A, HASH_A, validSignature));
  assert.equal(response.status, 401);
  assert.ok(!(await response.text()).includes("build-output"));
});

test("download label over different stored bytes is rejected", async () => {
  const backend = new MemoryBackend();
  const handler = createCacheSignatureHandler({ backend, secrets });
  const artifact = encode("build-output");

  await handler(
    makeRequest(
      "PUT",
      TEAM_A,
      HASH_A,
      signArtifact(KEY_A, TEAM_A, HASH_A, artifact),
      artifact,
    ),
  );

  const wrongArtifact = encode("other-bytes!!!");
  const response = await handler(
    makeRequest("GET", TEAM_A, HASH_A, signArtifact(KEY_A, TEAM_A, HASH_A, wrongArtifact)),
  );
  assert.equal(response.status, 401);
});

test("a valid-looking but mismatched label is rejected without storing bytes", async () => {
  const backend = new MemoryBackend();
  const handler = createCacheSignatureHandler({ backend, secrets });
  const artifact = encode("build-output");

  const response = await handler(
    makeRequest("PUT", TEAM_A, HASH_A, "f".repeat(64), artifact),
  );
  assert.equal(response.status, 401);
  assert.equal(backend.storeCalls.length, 0);
});

test("invalid task hash, route shape, and method are rejected", async () => {
  const backend = new MemoryBackend();
  const handler = createCacheSignatureHandler({ backend, secrets });

  const badHash = makeRequest("PUT", TEAM_A, "0123456789ABCDEF", "0".repeat(64), encode("x"));
  assert.equal((await handler(badHash)).status, 400);

  const shortHash = makeRequest("PUT", TEAM_A, "0123456789abcde", "0".repeat(64), encode("x"));
  assert.equal((await handler(shortHash)).status, 400);

  const wrongPath = new Request(
    "https://cache.example.com/cache/other/team/0123456789abcdef",
    { method: "GET", headers: { [SIGNATURE_HEADER]: "0".repeat(64) } },
  );
  assert.equal((await handler(wrongPath)).status, 400);

  const wrongMethod = new Request(cacheUrl(TEAM_A, HASH_A), {
    method: "POST",
    headers: { [SIGNATURE_HEADER]: "0".repeat(64) },
  });
  assert.equal((await handler(wrongMethod)).status, 405);
});
