import assert from "node:assert/strict";
import test from "node:test";
import {
  buildRepresentation,
  digestHex,
  normalizeManifest,
  StreamingHasher,
  verifyRepresentation,
} from "../src/index.ts";
import { text } from "./helpers.ts";

test("a weak match cannot mask a strong digest mismatch", () => {
  const bytes = text("strong digest group");
  const representation = buildRepresentation("decoded", bytes, {
    domain: "decoded.v1",
    algorithms: ["sha256", "sha512"],
  });
  const tampered = structuredClone(representation);
  const strong = tampered.groups[0].entries.find(
    (entry) => entry.algorithm === "sha512",
  );
  assert.ok(strong);
  strong.digest = "0".repeat(128);

  const result = verifyRepresentation(tampered, bytes, { kind: "decoded" });
  assert.equal(result.ok, false);
  assert.equal(result.groups[0].selectedAlgorithm, "sha512");
  assert.equal(result.groups[0].checks.length, 1);
  assert.equal(result.groups[0].checks[0].matched, false);
  assert.equal(result.groups[0].reason, "DIGEST_MISMATCH");
});

test("unsupported future algorithms are ignored, strongest supported is used", () => {
  const bytes = text("algorithm agility");
  const manifest = normalizeManifest({
    transport: {
      domain: "transport.v1",
      length: bytes.byteLength,
      algorithm: "sha256",
      digest: digestHex("sha256", "transport.v1", bytes),
    },
    decoded: {
      domain: "decoded.v1",
      length: bytes.byteLength,
      groups: [
        {
          entries: [
            { algorithm: "sha3-512", digest: "ab".repeat(64) },
            {
              algorithm: "sha256",
              digest: digestHex("sha256", "decoded.v1", bytes),
            },
          ],
        },
      ],
    },
  });

  const result = verifyRepresentation(manifest.decoded, bytes, {
    kind: "decoded",
  });
  assert.equal(result.ok, true);
  assert.equal(result.groups[0].selectedAlgorithm, "sha256");
  assert.equal(result.groups[0].checks[0].matched, true);
});

test("each independent digest group must pass", () => {
  const bytes = text("independent groups");
  const representation = buildRepresentation("decoded", bytes, {
    domain: "decoded.v1",
    groups: [["sha256"], ["sha512"]],
  });
  const tampered = structuredClone(representation);
  tampered.groups[1].entries[0].digest = "f".repeat(128);

  const result = verifyRepresentation(tampered, bytes, { kind: "decoded" });
  assert.equal(result.ok, false);
  assert.equal(result.groups[0].ok, true);
  assert.equal(result.groups[1].ok, false);
  assert.equal(result.groups[1].selectedAlgorithm, "sha512");
});

test("transport digests never authenticate decoded bytes", () => {
  const transportBytes = text("wire bytes");
  const decodedBytes = text("evil bytes");
  const transport = buildRepresentation("transport", transportBytes, {
    domain: "transport.v1",
    algorithms: ["sha256"],
  });

  const result = verifyRepresentation(transport, decodedBytes);
  assert.equal(result.ok, false);
  assert.ok(result.failures.includes("group:group-0:DIGEST_MISMATCH"));
});

test("domain tags and lengths are part of authentication", () => {
  const bytes = text("domain and length");
  assert.notEqual(
    digestHex("sha256", "domain-a", bytes),
    digestHex("sha256", "domain-b", bytes),
  );

  const representation = buildRepresentation("transport", bytes, {
    domain: "domain-a",
    algorithms: ["sha256"],
  });
  const wrongLength = verifyRepresentation(
    representation,
    new Uint8Array([...bytes, 0]),
    { kind: "transport" },
  );
  assert.equal(wrongLength.ok, false);
  assert.ok(wrongLength.failures.includes("length-mismatch"));
});

test("a group can override the representation domain but remains length checked", () => {
  const bytes = text("group domain");
  const manifest = normalizeManifest({
    transport: {
      domain: "transport.v1",
      length: bytes.byteLength,
      algorithm: "sha256",
      digest: digestHex("sha256", "transport.v1", bytes),
    },
    decoded: {
      domain: "decoded.v1",
      length: bytes.byteLength,
      groups: [
        {
          domain: "decoded.special.v1",
          length: bytes.byteLength,
          entries: [
            {
              algorithm: "sha256",
              digest: digestHex("sha256", "decoded.special.v1", bytes),
            },
          ],
        },
      ],
    },
  });
  const result = verifyRepresentation(manifest.decoded, bytes, {
    kind: "decoded",
  });
  assert.equal(result.ok, true);
  assert.equal(result.groups[0].checks[0].domain, "decoded.special.v1");
});

test("streaming hasher matches one-shot hashing at every cut point", () => {
  const bytes = text("streaming hash ".repeat(30));
  const expected = digestHex("sha512", "stream.domain", bytes);
  for (let cut = 0; cut <= bytes.byteLength; cut += 1) {
    const hasher = new StreamingHasher("sha512", "stream.domain", {
      expectedLength: bytes.byteLength,
    });
    hasher.update(bytes.slice(0, cut));
    hasher.update(bytes.slice(cut));
    assert.equal(hasher.digestHex(), expected, `cut ${cut}`);
  }
});

test("nested digests declarations become independent groups", () => {
  const bytes = text("nested groups");
  const manifest = normalizeManifest({
    transport: {
      domain: "transport.v1",
      length: bytes.byteLength,
      algorithm: "sha256",
      digest: digestHex("sha256", "transport.v1", bytes),
    },
    decoded: {
      domain: "decoded.v1",
      length: bytes.byteLength,
      digests: [
        [
          {
            algorithm: "sha256",
            digest: digestHex("sha256", "decoded.v1", bytes),
          },
        ],
        [
          {
            algorithm: "sha512",
            digest: digestHex("sha512", "decoded.v1", bytes),
          },
        ],
      ],
    },
  });
  assert.equal(manifest.decoded.groups.length, 2);
  const result = verifyRepresentation(manifest.decoded, bytes, {
    kind: "decoded",
  });
  assert.equal(result.ok, true);
  assert.equal(result.groups.length, 2);
});
