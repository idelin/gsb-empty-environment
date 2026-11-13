import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CERTIFICATE_CLOCK_SKEW_MS,
  verifySenderCertificate,
  type SenderCertificate,
  type TrustedRoot,
} from "../src/certificate.ts";
import {
  CertificateError,
  CertificateExpiredError,
  isCertificateExpiredError,
} from "../src/errors.ts";
import {
  bytes,
  clone,
  flipLastByte,
  generateP256KeyPair,
  signP256,
  type P256KeyPair,
} from "./helpers.ts";

const NOW = 1_000_000_000_000;
const IDENTITY = bytes("session-identity-key");
const SENDER = "alice:42";

interface TestMaterial {
  root: P256KeyPair;
  trustedRoot: TrustedRoot;
  certificate: SenderCertificate;
}

async function makeCertificate(
  overrides: Partial<SenderCertificate> = {},
): Promise<TestMaterial> {
  const root = await generateP256KeyPair();
  const signedData = bytes("canonical-certificate-payload");
  const signature = await signP256(root.privateKey, signedData);
  const certificate: SenderCertificate = {
    sender: SENDER,
    identityKey: clone(IDENTITY),
    validFrom: NOW - 60_000,
    validUntil: NOW + 60_000,
    signedData,
    signature,
    rootId: "root-2026",
    ...overrides,
  };
  const trustedRoot: TrustedRoot = {
    rootId: "root-2026",
    publicKey: root.publicKeySpki,
  };
  return { root, trustedRoot, certificate };
}

function baseOptions(
  material: TestMaterial,
  overrides: Parameters<typeof verifySenderCertificate>[0] = {} as never,
) {
  return {
    certificate: material.certificate,
    sessionIdentityKey: clone(IDENTITY),
    declaredSender: SENDER,
    trustedRoots: [material.trustedRoot],
    now: NOW,
    ...overrides,
  };
}

test("valid certificate issued by a trusted root passes", async () => {
  const material = await makeCertificate();
  await assert.doesNotReject(verifySenderCertificate(baseOptions(material)));
});

test("certificate signed by an untrusted root is rejected", async () => {
  const material = await makeCertificate();
  const other = await generateP256KeyPair();
  await assert.rejects(
    verifySenderCertificate(
      baseOptions(material, {
        trustedRoots: [{ rootId: "root-2026", publicKey: other.publicKeySpki }],
      }),
    ),
    (error: unknown) =>
      error instanceof CertificateError &&
      error.code === "CERT_UNTRUSTED_ROOT",
  );
});

test("tampered signature is rejected as untrusted", async () => {
  const material = await makeCertificate();
  material.certificate.signature = flipLastByte(material.certificate.signature);
  await assert.rejects(
    verifySenderCertificate(baseOptions(material)),
    (error: unknown) =>
      error instanceof CertificateError &&
      error.code === "CERT_UNTRUSTED_ROOT",
  );
});

test("matching root among several trusted roots verifies", async () => {
  const material = await makeCertificate();
  const decoy = await generateP256KeyPair();
  await assert.doesNotReject(
    verifySenderCertificate(
      baseOptions(material, {
        trustedRoots: [
          { rootId: "decoy", publicKey: decoy.publicKeySpki },
          material.trustedRoot,
        ],
      }),
    ),
  );
});

test("unknown rootId is rejected", async () => {
  const material = await makeCertificate();
  await assert.rejects(
    verifySenderCertificate(
      baseOptions(material, {
        trustedRoots: [{ rootId: "other-root", publicKey: material.root.publicKeySpki }],
      }),
    ),
    (error: unknown) =>
      error instanceof CertificateError &&
      error.code === "CERT_UNTRUSTED_ROOT",
  );
});

test("expired beyond the 5-minute tolerance raises the silent expiry error", async () => {
  const material = await makeCertificate({ validUntil: NOW - CERTIFICATE_CLOCK_SKEW_MS - 1 });
  await assert.rejects(
    verifySenderCertificate(baseOptions(material)),
    (error: unknown) => isCertificateExpiredError(error),
  );
});

test("expired within the 5-minute tolerance is still accepted", async () => {
  const material = await makeCertificate({
    validUntil: NOW - CERTIFICATE_CLOCK_SKEW_MS,
  });
  await assert.doesNotReject(verifySenderCertificate(baseOptions(material)));
});

test("expiry boundary is inclusive of the tolerance", async () => {
  const material = await makeCertificate({
    validUntil: NOW - CERTIFICATE_CLOCK_SKEW_MS + 1,
  });
  await assert.doesNotReject(verifySenderCertificate(baseOptions(material)));
});

test("not-yet-valid beyond tolerance raises the expiry error", async () => {
  const material = await makeCertificate({
    validFrom: NOW + CERTIFICATE_CLOCK_SKEW_MS + 1,
  });
  await assert.rejects(
    verifySenderCertificate(baseOptions(material)),
    (error: unknown) => error instanceof CertificateExpiredError,
  );
});

test("custom clock skew is honored", async () => {
  const material = await makeCertificate({ validUntil: NOW - 60_000 });
  await assert.doesNotReject(
    verifySenderCertificate(baseOptions(material, { clockSkewMs: 90_000 })),
  );
});

test("session identity key mismatch is rejected", async () => {
  const material = await makeCertificate();
  await assert.rejects(
    verifySenderCertificate(
      baseOptions(material, { sessionIdentityKey: bytes("different-identity") }),
    ),
    (error: unknown) =>
      error instanceof CertificateError &&
      error.code === "CERT_IDENTITY_MISMATCH",
  );
});

test("declared sender mismatch is rejected", async () => {
  const material = await makeCertificate();
  await assert.rejects(
    verifySenderCertificate(
      baseOptions(material, { declaredSender: "mallory:99" }),
    ),
    (error: unknown) =>
      error instanceof CertificateError &&
      error.code === "CERT_SENDER_MISMATCH",
  );
});

test("sender normalization applies to both identifiers", async () => {
  const material = await makeCertificate({ sender: "Alice:42" });
  await assert.doesNotReject(
    verifySenderCertificate(
      baseOptions(material, {
        declaredSender: "alice:42",
        normalizeSender: (value) => value.toLowerCase(),
      }),
    ),
  );
});

test("malformed certificate structures are rejected", async () => {
  const material = await makeCertificate();
  const malformed = { ...material.certificate, identityKey: new Uint8Array(0) };
  await assert.rejects(
    verifySenderCertificate(baseOptions(material, { certificate: malformed })),
    (error: unknown) =>
      error instanceof CertificateError && error.code === "CERT_MALFORMED",
  );
});
