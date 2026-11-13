import { test } from "node:test";
import assert from "node:assert/strict";
import {
  processSealedSender,
  isSilentDrop,
  type ProcessSealedSenderOptions,
  type SealedSenderMessage,
} from "../src/sealed-sender.ts";
import type {
  AccessKeyRecord,
  AccessKeyRegistry,
} from "../src/access-key.ts";
import type { SenderCertificate, TrustedRoot } from "../src/certificate.ts";
import { AccessKeyError, CertificateError } from "../src/errors.ts";
import {
  bytes,
  clone,
  generateP256KeyPair,
  signP256,
} from "./helpers.ts";

const NOW = 2_000_000_000_000;
const ACCESS_KEY = new Uint8Array(16).fill(11);
const IDENTITY = bytes("identity-key");
const SENDER = "alice:7";

function registryFactory(record?: AccessKeyRecord): {
  registry: AccessKeyRegistry;
  calls: string[];
} {
  const calls: string[] = [];
  return {
    calls,
    registry: async (recipientId: string) => {
      calls.push(recipientId);
      return record;
    },
  };
}

async function makeValidMessage(): Promise<{
  message: SealedSenderMessage;
  trustedRoots: TrustedRoot[];
}> {
  const root = await generateP256KeyPair();
  const signedData = bytes("cert-payload");
  const signature = await signP256(root.privateKey, signedData);
  const certificate: SenderCertificate = {
    sender: SENDER,
    identityKey: clone(IDENTITY),
    validFrom: NOW - 60_000,
    validUntil: NOW + 60_000,
    signedData,
    signature,
    rootId: "root-1",
  };
  const message: SealedSenderMessage = {
    unidentifiedAccessKey: clone(ACCESS_KEY),
    recipientId: "bob",
    senderCertificate: certificate,
    declaredSender: SENDER,
    sessionIdentityKey: clone(IDENTITY),
  };
  return {
    message,
    trustedRoots: [{ rootId: "root-1", publicKey: root.publicKeySpki }],
  };
}

function options(
  message: SealedSenderMessage,
  trustedRoots: TrustedRoot[],
  registry: AccessKeyRegistry,
): ProcessSealedSenderOptions {
  return { message, trustedRoots, registry, now: NOW };
}

test("fully valid sealed-sender message is accepted and receipt-allowed", async () => {
  const { message, trustedRoots } = await makeValidMessage();
  const { registry } = registryFactory({ recipientId: "bob", accessKey: ACCESS_KEY });
  const result = await processSealedSender(options(message, trustedRoots, registry));
  assert.equal(result.accepted, true);
  if (result.accepted) {
    assert.equal(result.deliveryReceiptAllowed, true);
    assert.equal(result.sender, SENDER);
  }
});

test("wrong access key rejects uniformly and is never silent", async () => {
  const { message, trustedRoots } = await makeValidMessage();
  const { registry } = registryFactory({ recipientId: "bob", accessKey: ACCESS_KEY });
  const result = await processSealedSender(
    options({ ...message, unidentifiedAccessKey: new Uint8Array(16).fill(1) }, trustedRoots, registry),
  );
  assert.equal(result.accepted, false);
  if (!result.accepted) {
    assert.equal(result.reason, "ACCESS_KEY_REJECTED");
    assert.ok(result.error instanceof AccessKeyError);
    assert.equal(result.silent, false);
    assert.equal(isSilentDrop(result), false);
  }
});

test("expired certificate is a silent drop with no receipt", async () => {
  const { message, trustedRoots } = await makeValidMessage();
  message.senderCertificate.validUntil = NOW - 6 * 60 * 1000;
  const { registry } = registryFactory({ recipientId: "bob", accessKey: ACCESS_KEY });
  const result = await processSealedSender(options(message, trustedRoots, registry));
  assert.equal(result.accepted, false);
  if (!result.accepted) {
    assert.equal(result.reason, "CERT_EXPIRED");
    assert.equal(result.silent, true);
    assert.equal(isSilentDrop(result), true);
  }
});

test("untrusted certificate rejects loudly (not silent)", async () => {
  const { message } = await makeValidMessage();
  const other = await generateP256KeyPair();
  const { registry } = registryFactory({ recipientId: "bob", accessKey: ACCESS_KEY });
  const result = await processSealedSender(
    options(message, [{ rootId: "root-1", publicKey: other.publicKeySpki }], registry),
  );
  assert.equal(result.accepted, false);
  if (!result.accepted) {
    assert.equal(result.reason, "CERT_UNTRUSTED_ROOT");
    assert.equal(result.silent, false);
  }
});

test("identity mismatch rejects with the dedicated code", async () => {
  const { message, trustedRoots } = await makeValidMessage();
  const { registry } = registryFactory({ recipientId: "bob", accessKey: ACCESS_KEY });
  const result = await processSealedSender(
    options(
      { ...message, sessionIdentityKey: bytes("spoofed-identity") },
      trustedRoots,
      registry,
    ),
  );
  assert.equal(result.accepted, false);
  if (!result.accepted) {
    assert.equal(result.reason, "CERT_IDENTITY_MISMATCH");
    assert.ok(result.error instanceof CertificateError);
    assert.equal(result.silent, false);
  }
});

test("sender mismatch rejects with the dedicated code", async () => {
  const { message, trustedRoots } = await makeValidMessage();
  const { registry } = registryFactory({ recipientId: "bob", accessKey: ACCESS_KEY });
  const result = await processSealedSender(
    options({ ...message, declaredSender: "mallory:1" }, trustedRoots, registry),
  );
  assert.equal(result.accepted, false);
  if (!result.accepted) {
    assert.equal(result.reason, "CERT_SENDER_MISMATCH");
    assert.equal(result.silent, false);
  }
});

test("access key is checked before certificate validation", async () => {
  const { message, trustedRoots } = await makeValidMessage();
  message.senderCertificate.validUntil = NOW - 6 * 60 * 1000;
  const { registry } = registryFactory({ recipientId: "bob", accessKey: ACCESS_KEY });
  const result = await processSealedSender(
    options(
      { ...message, unidentifiedAccessKey: new Uint8Array(16).fill(2) },
      trustedRoots,
      registry,
    ),
  );
  assert.equal(result.accepted, false);
  if (!result.accepted) {
    assert.equal(result.reason, "ACCESS_KEY_REJECTED");
  }
});

test("unknown recipient and bad key are indistinguishable", async () => {
  const { message, trustedRoots } = await makeValidMessage();
  const { registry } = registryFactory(undefined);
  const result = await processSealedSender(options(message, trustedRoots, registry));
  assert.equal(result.accepted, false);
  if (!result.accepted) {
    assert.equal(result.reason, "ACCESS_KEY_REJECTED");
    assert.equal(result.error.message, "unidentified access key rejected");
  }
});
