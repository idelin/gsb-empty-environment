import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import {
  filterEnterpriseAttestation,
  filterRegistrationAttestation,
  type AttestationFilterPolicy,
  type RegistrationAttestationRequest,
  type RegistrationAttestationResponse,
} from "./index.ts";

const MANAGED_AAGUID = "d6f2b6c8-4a27-4f1e-9d57-6b75a7f4c33a";
const OTHER_AAGUID = "11111111-2222-3333-4444-555555555555";
const AAGUID_OID = "1.3.6.1.4.1.45724.1.1.4";
const SUBJECT_SERIAL_OID = "2.5.4.5";
const RP_ID = "login.example.com";
const SERIAL_NUMBER = "SN-CORP-0001";

const policy: AttestationFilterPolicy = {
  adminRpIds: [RP_ID],
  managedAaguids: [MANAGED_AAGUID],
};

interface RegistrationFixture {
  request: RegistrationAttestationRequest;
  response: RegistrationAttestationResponse;
  certificate: string;
}

function makeRegistration(options: {
  rpId?: string;
  requestedFormats?: string[];
  responseFormat?: string;
  authenticatorAaguid?: string;
  certificateAaguid?: string;
  includeSubjectSerial?: boolean;
} = {}): RegistrationFixture {
  const rpId = options.rpId ?? RP_ID;
  const authenticatorAaguid = options.authenticatorAaguid ?? MANAGED_AAGUID;
  const certificateAaguid = options.certificateAaguid ?? authenticatorAaguid;
  const certificateDer = makeCertificate({
    aaguid: certificateAaguid,
    subjectSerialNumber:
      options.includeSubjectSerial === false ? null : SERIAL_NUMBER,
  });
  const certificate = encodeBase64Url(certificateDer);
  const authData = makeAuthData(rpId, authenticatorAaguid);
  const attestationObject = encodeCbor(
    new Map<CborKey, CborValue>([
      ["fmt", options.responseFormat ?? "packed"],
      [
        "attStmt",
        new Map<CborKey, CborValue>([
          ["alg", -7],
          ["sig", new Uint8Array([1, 2, 3])],
          ["x5c", [certificate]],
        ]),
      ],
      ["authData", authData],
    ]),
  );

  return {
    request: {
      rpId,
      attestation: "enterprise",
      attestationFormats: options.requestedFormats ?? ["packed"],
    },
    response: {
      attestationObject: encodeBase64Url(attestationObject),
    },
    certificate,
  };
}

test("retains certificate and serial number for a managed enterprise registration", () => {
  const fixture = makeRegistration();
  const record = filterEnterpriseAttestation(fixture, policy);

  assert.deepEqual(record, {
    attestation: "enterprise",
    fmt: "packed",
    certificate: fixture.certificate,
    serialNumber: SERIAL_NUMBER,
    aaguid: MANAGED_AAGUID,
    downgradeReason: null,
  });
});

test("downgrades registration when the RP ID is not on the administrator list", () => {
  const fixture = makeRegistration();
  const record = filterRegistrationAttestation(fixture.request, fixture.response, {
    ...policy,
    adminRpIds: ["admin.example.com"],
  });

  assert.deepEqual(record, {
    attestation: "none",
    fmt: "none",
    certificate: null,
    serialNumber: null,
    aaguid: null,
    downgradeReason: "rp-id-not-allowed",
  });
});

test("downgrades registration when request and response attestation formats differ", () => {
  const fixture = makeRegistration({ requestedFormats: ["fido2"] });
  const record = filterEnterpriseAttestation(fixture, policy);

  assert.equal(record.attestation, "none");
  assert.equal(record.downgradeReason, "attestation-format-mismatch");
  assert.equal(record.certificate, null);
  assert.equal(record.serialNumber, null);
  assert.equal(record.aaguid, null);
});

test("downgrades registration when the certificate AAGUID is not managed", () => {
  const fixture = makeRegistration({
    authenticatorAaguid: OTHER_AAGUID,
    certificateAaguid: OTHER_AAGUID,
  });
  const record = filterEnterpriseAttestation(fixture, policy);

  assert.equal(record.attestation, "none");
  assert.equal(record.downgradeReason, "aaguid-not-managed");
  assert.equal(record.certificate, null);
  assert.equal(record.serialNumber, null);
  assert.equal(record.aaguid, null);
});

test("downgrades registration when certificate and authenticator AAGUIDs differ", () => {
  const fixture = makeRegistration({ certificateAaguid: OTHER_AAGUID });
  const record = filterEnterpriseAttestation(fixture, policy);

  assert.equal(record.attestation, "none");
  assert.equal(record.downgradeReason, "aaguid-mismatch");
  assert.equal(record.certificate, null);
  assert.equal(record.serialNumber, null);
  assert.equal(record.aaguid, null);
});

test("downgrades ordinary-site registrations even if enterprise data is present", () => {
  const fixture = makeRegistration();
  fixture.request.attestation = "none";
  const record = filterEnterpriseAttestation(fixture, policy);

  assert.equal(record.attestation, "none");
  assert.equal(record.downgradeReason, "enterprise-attestation-not-requested");
  assert.equal(record.certificate, null);
  assert.equal(record.serialNumber, null);
  assert.equal(record.aaguid, null);
});

test("accepts an already parsed response without an attestationObject", () => {
  const fixture = makeRegistration({ includeSubjectSerial: false });
  const parsed = parseFixtureResponse(fixture);
  const record = filterEnterpriseAttestation(
    {
      ...fixture,
      response: {
        fmt: parsed.fmt,
        attStmt: parsed.attStmt,
        authData: parsed.authData,
        serialNumber: "CERT-SERIAL-42",
      },
    },
    policy,
  );

  assert.equal(record.attestation, "enterprise");
  assert.equal(record.serialNumber, "019a2b3c");
  assert.equal(record.certificate, fixture.certificate);
});

function parseFixtureResponse(fixture: RegistrationFixture): {
  fmt: string;
  attStmt: Map<unknown, unknown>;
  authData: Uint8Array;
} {
  const bytes = decodeBase64Url(fixture.response.attestationObject as string);
  const decoded = decodeCbor(bytes) as Map<string, CborValue>;
  return {
    fmt: decoded.get("fmt") as string,
    attStmt: decoded.get("attStmt") as Map<unknown, unknown>,
    authData: decoded.get("authData") as Uint8Array,
  };
}

function makeCertificate(options: {
  aaguid: string;
  subjectSerialNumber: string | null;
}): Uint8Array {
  const serial = der(0x02, new Uint8Array([0x01, 0x9a, 0x2b, 0x3c]));
  const signatureAlgorithm = sequence(
    oid("1.2.840.113549.1.1.11"),
    der(0x05, new Uint8Array()),
  );
  const issuer = makeName(options.subjectSerialNumber);
  const subject = makeName(options.subjectSerialNumber);
  const validity = sequence(
    der(0x17, textBytes("250101000000Z")),
    der(0x17, textBytes("300101000000Z")),
  );
  const publicKeyInfo = sequence(
    sequence(
      oid("1.2.840.113549.1.1.1"),
      der(0x05, new Uint8Array()),
    ),
    der(0x03, new Uint8Array([0x00, 0x01, 0x02, 0x03])),
  );
  const aaguidExtension = sequence(
    oid(AAGUID_OID),
    der(
      0x04,
      der(0x04, uuidToBytes(options.aaguid)),
    ),
  );
  const extensions = der(0xa3, sequence(aaguidExtension));

  const tbsCertificate = sequence(
    der(0xa0, der(0x02, new Uint8Array([0x02]))),
    serial,
    signatureAlgorithm,
    issuer,
    validity,
    subject,
    publicKeyInfo,
    extensions,
  );
  const certificate = sequence(
    tbsCertificate,
    signatureAlgorithm,
    der(0x03, new Uint8Array([0x00, 0x04, 0x05, 0x06])),
  );
  return certificate;
}

function makeName(serialNumber: string | null): Uint8Array {
  const attributes = [
    sequence(
      oid("2.5.4.3"),
      der(0x13, textBytes("Managed Authenticator")),
    ),
  ];
  if (serialNumber) {
    attributes.push(
      sequence(
        oid(SUBJECT_SERIAL_OID),
        der(0x13, textBytes(serialNumber)),
      ),
    );
  }
  return sequence(...attributes.map((attribute) => der(0x31, attribute)));
}

function makeAuthData(rpId: string, aaguid: string): Uint8Array {
  const rpIdHash = new Uint8Array(
    createHash("sha256").update(rpId).digest(),
  );
  const flagsAndCounter = new Uint8Array([0x45, 0x00, 0x00, 0x00, 0x07]);
  const credentialId = new Uint8Array(16).fill(0x33);
  const credentialIdLength = new Uint8Array([
    credentialId.byteLength >> 8,
    credentialId.byteLength & 0xff,
  ]);
  return concatBytes(
    rpIdHash,
    flagsAndCounter,
    uuidToBytes(aaguid),
    credentialIdLength,
    credentialId,
    new Uint8Array([0xa0]),
  );
}

type CborKey = string | number | Uint8Array;
type CborValue =
  | string
  | number
  | boolean
  | null
  | Uint8Array
  | CborValue[]
  | Map<CborKey, CborValue>;

function encodeCbor(value: CborValue): Uint8Array {
  if (typeof value === "string") {
    const bytes = textBytes(value);
    return concatBytes(cborHeader(3, bytes.byteLength), bytes);
  }
  if (typeof value === "number") {
    if (value >= 0) return cborHeader(0, value);
    return cborHeader(1, -1 - value);
  }
  if (value === null) return new Uint8Array([0xf6]);
  if (value === true) return new Uint8Array([0xf5]);
  if (value === false) return new Uint8Array([0xf4]);
  if (value instanceof Uint8Array) {
    return concatBytes(cborHeader(2, value.byteLength), value);
  }
  if (Array.isArray(value)) {
    return concatBytes(
      cborHeader(4, value.length),
      ...value.map((entry) => encodeCbor(entry)),
    );
  }
  if (value instanceof Map) {
    const entries = Array.from(value.entries());
    return concatBytes(
      cborHeader(5, entries.length),
      ...entries.flatMap(([key, entry]) => [
        encodeCbor(key as CborValue),
        encodeCbor(entry),
      ]),
    );
  }
  throw new TypeError("Unsupported test CBOR value");
}

function cborHeader(major: number, value: number): Uint8Array {
  if (value < 24) return new Uint8Array([(major << 5) | value]);
  if (value < 256) {
    return new Uint8Array([(major << 5) | 24, value]);
  }
  if (value < 65536) {
    return new Uint8Array([
      (major << 5) | 25,
      value >> 8,
      value & 0xff,
    ]);
  }
  const bytes = new Uint8Array(5);
  bytes[0] = (major << 5) | 26;
  bytes[1] = (value >>> 24) & 0xff;
  bytes[2] = (value >>> 16) & 0xff;
  bytes[3] = (value >>> 8) & 0xff;
  bytes[4] = value & 0xff;
  return bytes;
}

function decodeCbor(data: Uint8Array): unknown {
  let offset = 0;
  const readLength = (additional: number): number => {
    if (additional < 24) return additional;
    if (additional === 24) {
      const value = data[offset];
      offset += 1;
      return value;
    }
    if (additional === 25) {
      const value = (data[offset] << 8) | data[offset + 1];
      offset += 2;
      return value;
    }
    throw new TypeError("Unsupported CBOR length");
  };

  const read = (): unknown => {
    const initial = data[offset];
    offset += 1;
    const major = initial >> 5;
    const additional = initial & 0x1f;
    const length = readLength(additional);
    if (major === 0) return length;
    if (major === 1) return -1 - length;
    if (major === 2) {
      const value = data.slice(offset, offset + length);
      offset += length;
      return value;
    }
    if (major === 3) {
      const value = new TextDecoder().decode(
        data.slice(offset, offset + length),
      );
      offset += length;
      return value;
    }
    if (major === 4) {
      return Array.from({ length }, () => read());
    }
    if (major === 5) {
      const result = new Map<unknown, unknown>();
      for (let index = 0; index < length; index += 1) {
        result.set(read(), read());
      }
      return result;
    }
    if (major === 7 && additional === 22) return null;
    throw new TypeError("Unsupported test CBOR value");
  };

  return read();
}

function decodeBase64Url(value: string): Uint8Array {
  return new Uint8Array(Buffer.from(value, "base64url"));
}

function encodeBase64Url(value: Uint8Array): string {
  return Buffer.from(value).toString("base64url");
}

function uuidToBytes(uuid: string): Uint8Array {
  const compact = uuid.replace(/-/g, "");
  const bytes = new Uint8Array(16);
  for (let index = 0; index < 16; index += 1) {
    bytes[index] = Number.parseInt(
      compact.slice(index * 2, index * 2 + 2),
      16,
    );
  }
  return bytes;
}

function textBytes(value: string): Uint8Array {
  return new TextEncoder().encode(value);
}

function der(tag: number, content: Uint8Array): Uint8Array {
  return concatBytes(new Uint8Array([tag]), derLength(content.byteLength), content);
}

function derLength(length: number): Uint8Array {
  if (length < 0x80) return new Uint8Array([length]);
  const bytes: number[] = [];
  let remaining = length;
  while (remaining > 0) {
    bytes.unshift(remaining & 0xff);
    remaining >>= 8;
  }
  return new Uint8Array([0x80 | bytes.length, ...bytes]);
}

function oid(value: string): Uint8Array {
  const parts = value.split(".").map(Number);
  const bytes = [parts[0] * 40 + parts[1]];
  for (const part of parts.slice(2)) {
    const encoded: number[] = [part & 0x7f];
    let remaining = Math.floor(part / 128);
    while (remaining > 0) {
      encoded.unshift((remaining & 0x7f) | 0x80);
      remaining = Math.floor(remaining / 128);
    }
    bytes.push(...encoded);
  }
  return der(0x06, new Uint8Array(bytes));
}

function sequence(...content: Uint8Array[]): Uint8Array {
  return der(0x30, concatBytes(...content));
}

function concatBytes(...values: Uint8Array[]): Uint8Array {
  const length = values.reduce((total, value) => total + value.byteLength, 0);
  const result = new Uint8Array(length);
  let offset = 0;
  for (const value of values) {
    result.set(value, offset);
    offset += value.byteLength;
  }
  return result;
}
