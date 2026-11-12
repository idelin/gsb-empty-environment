import test from "node:test";
import assert from "node:assert/strict";
import { inspect } from "node:util";
import {
  assertExportSubkey,
  assertSessionSubkey,
  deriveLoginKeyHandles,
  type LoginKeyMaterial,
} from "./index.ts";

function bytes(seed: number, length = 32): Uint8Array {
  return Uint8Array.from({ length }, (_, index) => (seed + index) & 0xff);
}

function login(overrides: Partial<LoginKeyMaterial> = {}): LoginKeyMaterial {
  return {
    sessionKey: bytes(1),
    exportKey: bytes(65),
    clientIdentity: "alice@example.com",
    serverIdentity: "api.example.com",
    transcriptHash: bytes(129),
    ...overrides,
  };
}

test("derives distinct purpose-bound handles and consumes the raw login keys", async () => {
  const material = login();
  const handles = await deriveLoginKeyHandles(material);

  assert.equal(handles.session.purpose, "session");
  assert.equal(handles.export.purpose, "export");
  assert.equal(handles.session.algorithm, "AES-GCM");
  assert.equal(handles.export.algorithm, "AES-GCM");
  assert.ok(Object.isFrozen(handles));
  assert.ok(Object.isFrozen(handles.session));
  assert.ok(Object.isFrozen(handles.export));
  assert.ok(material.sessionKey instanceof Uint8Array);
  assert.ok(material.exportKey instanceof Uint8Array);
  assert.equal(material.sessionKey.every((value) => value === 0), true);
  assert.equal(material.exportKey.every((value) => value === 0), true);

  assertSessionSubkey(handles.session);
  assertExportSubkey(handles.export);
  assert.throws(() => assertSessionSubkey(handles.export), /session subkey handle/);
  assert.throws(() => assertExportSubkey(handles.session), /export subkey handle/);
  assert.throws(() => {
    (handles.export as unknown as { purpose: string }).purpose = "session";
  }, /read only|Cannot assign/i);
});

test("subkeys are non-extractable and cannot be exported as raw bytes", async () => {
  const handles = await deriveLoginKeyHandles(login());

  for (const handle of [handles.session, handles.export]) {
    await handle.withKey(async (key) => {
      assert.equal(key.extractable, false);
      assert.deepEqual([...key.usages], ["encrypt", "decrypt"]);
      await assert.rejects(
        crypto.subtle.exportKey("raw", key),
        /extractable|InvalidAccessError/i,
      );
    });
  }
});

test("same login is deterministic while labels and bound context separate the keys", async () => {
  const first = await deriveLoginKeyHandles(login());
  const second = await deriveLoginKeyHandles(login());
  const otherIdentity = await deriveLoginKeyHandles(
    login({ clientIdentity: "mallory@example.com" }),
  );
  const otherTranscript = await deriveLoginKeyHandles(
    login({ transcriptHash: bytes(161) }),
  );

  const iv = Uint8Array.from({ length: 12 }, (_, index) => index + 1);
  const plaintext = new TextEncoder().encode("opaque session payload");
  const ciphertext = await first.session.withKey((key) =>
    crypto.subtle.encrypt({ name: "AES-GCM", iv }, key, plaintext),
  );

  const decrypted = await second.session.withKey((key) =>
    crypto.subtle.decrypt({ name: "AES-GCM", iv }, key, ciphertext),
  );
  assert.deepEqual(
    new TextDecoder().decode(decrypted),
    "opaque session payload",
  );

  await assert.rejects(
    first.export.withKey((key) =>
      crypto.subtle.decrypt({ name: "AES-GCM", iv }, key, ciphertext),
    ),
    /OperationError|decrypt/i,
  );
  await assert.rejects(
    otherIdentity.export.withKey((key) =>
      crypto.subtle.decrypt({ name: "AES-GCM", iv }, key, ciphertext),
    ),
    /OperationError|decrypt/i,
  );
  await assert.rejects(
    otherTranscript.export.withKey((key) =>
      crypto.subtle.decrypt({ name: "AES-GCM", iv }, key, ciphertext),
    ),
    /OperationError|decrypt/i,
  );
});

test("accepts the OPAQUE snake_case login fields", async () => {
  const handles = await deriveLoginKeyHandles({
    session_key: bytes(1),
    export_key: bytes(65),
    client_identity: "alice@example.com",
    server_identity: "api.example.com",
    transcript_hash: bytes(129),
  });

  assert.equal(handles.session.purpose, "session");
  assert.equal(handles.export.purpose, "export");
  assertSessionSubkey(handles.session);
  assertExportSubkey(handles.export);
});

test("JSON, logs, requests, and structured clone cannot serialize key material", async () => {
  const handles = await deriveLoginKeyHandles(login());

  assert.throws(() => JSON.stringify(handles), /serialized to JSON/);
  assert.throws(() => JSON.stringify(handles.session), /serialized to JSON/);
  assert.throws(() => String(handles.session), /primitive/);
  assert.throws(() => `${handles.session}`, /primitive/);
  assert.throws(() => handles.session.toJSON(), /serialized to JSON/);

  assert.equal(
    inspect(handles.session),
    "DerivedSubkeyHandle { purpose: 'session', algorithm: 'AES-GCM', extractable: false, destroyed: false }",
  );
  const rendered = inspect(handles, { depth: 10, showHidden: true });
  assert.match(rendered, /purpose: 'session'/);
  assert.match(rendered, /purpose: 'export'/);
  assert.doesNotMatch(rendered, /Uint8Array|CryptoKey|rawKey|#key/);

  assert.throws(
    () => new Request("https://example.invalid/vault", { body: handles.export as never }),
    /body|BodyInit|convert/i,
  );
  assert.throws(
    () => new URLSearchParams({ key: handles.session as never }).toString(),
    /converted to text|primitive/i,
  );
  const clone = structuredClone(handles.session) as unknown as Record<string, unknown>;
  assert.equal(clone.withKey, undefined);
  assert.equal(clone.key, undefined);
  assert.equal(clone.raw, undefined);

  assert.deepEqual(Object.keys(handles.session), ["purpose", "algorithm", "length"]);
  assert.equal("key" in handles.session, false);
  assert.equal("raw" in handles.session, false);
  assert.equal((handles.session as unknown as Record<string, unknown>).key, undefined);
});

test("rejects identical raw keys and still wipes them", async () => {
  const same = bytes(1);
  await assert.rejects(
    deriveLoginKeyHandles(
      login({ sessionKey: same, exportKey: same }),
    ),
    /must be different/,
  );
  assert.equal(same.every((value) => value === 0), true);
});

test("destroyed handles refuse further use", async () => {
  const handles = await deriveLoginKeyHandles(login());
  handles.session.destroy();

  assert.equal(handles.session.destroyed, true);
  await assert.rejects(
    handles.session.withKey((key) => key),
    /destroyed/,
  );
  await assert.rejects(handles.session.withKey(null as never), /callback/);
});
