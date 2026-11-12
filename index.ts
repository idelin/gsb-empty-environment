export type OpaqueKeyPurpose = "session" | "export";

export type KeyBytes = Uint8Array | ArrayBuffer;

export type Identity = string | Uint8Array;

export interface LoginKeyMaterial {
  readonly sessionKey: KeyBytes;
  readonly exportKey: KeyBytes;
  readonly clientIdentity: Identity;
  readonly serverIdentity: Identity;
  readonly transcriptHash: Uint8Array;
}

export interface LoginKeyMaterialSnake {
  readonly session_key: KeyBytes;
  readonly export_key: KeyBytes;
  readonly client_identity: Identity;
  readonly server_identity: Identity;
  readonly transcript_hash: Uint8Array;
}

export type LoginKeyInput = LoginKeyMaterial | LoginKeyMaterialSnake;

export interface LoginKeyHandles {
  readonly session: DerivedSubkeyHandle<"session">;
  readonly export: DerivedSubkeyHandle<"export">;
}

export const SUBKEY_CONTEXT_DOMAIN = "opaque-client-sdk/key-handle/v1";

export const SUBKEY_LABELS = Object.freeze({
  session: "opaque-client-sdk/session-subkey/v1",
  export: "opaque-client-sdk/export-subkey/v1",
} as const);

const HANDLE_BRAND = Symbol("opaque-client-sdk.derived-subkey-handle");
const INSPECT_CUSTOM = Symbol.for("nodejs.util.inspect.custom");
const encoder = new TextEncoder();

export class DerivedSubkeyHandle<TPurpose extends OpaqueKeyPurpose> {
  readonly purpose: TPurpose;
  readonly algorithm = "AES-GCM";
  readonly length = 256;

  #key: CryptoKey | undefined;
  #destroyed = false;

  constructor(token: typeof HANDLE_BRAND, purpose: TPurpose, key: CryptoKey) {
    if (token !== HANDLE_BRAND) {
      throw new TypeError("Use deriveLoginKeyHandles to create key handles");
    }
    if (key.type !== "secret" || key.extractable !== false) {
      throw new TypeError("Derived subkeys must be non-extractable secret keys");
    }
    this.purpose = purpose;
    this.#key = key;
  }

  get destroyed(): boolean {
    return this.#destroyed;
  }

  get [Symbol.toStringTag](): string {
    return "DerivedSubkeyHandle";
  }

  async withKey<T>(consumer: (key: CryptoKey) => T | Promise<T>): Promise<T> {
    if (typeof consumer !== "function") {
      throw new TypeError("A key consumer callback is required");
    }
    const key = this.#key;
    if (this.#destroyed || key === undefined) {
      throw new TypeError("The derived subkey handle has been destroyed");
    }
    return await consumer(key);
  }

  destroy(): void {
    this.#key = undefined;
    this.#destroyed = true;
  }

  toJSON(): never {
    throw new TypeError("Derived subkeys cannot be serialized to JSON");
  }

  toString(): never {
    throw new TypeError("Derived subkeys cannot be converted to text");
  }

  valueOf(): never {
    throw new TypeError("Derived subkeys cannot be converted to a primitive");
  }

  [Symbol.toPrimitive](): never {
    throw new TypeError("Derived subkeys cannot be converted to a primitive");
  }

  [INSPECT_CUSTOM](): string {
    return `DerivedSubkeyHandle { purpose: '${this.purpose}', algorithm: '${this.algorithm}', extractable: false, destroyed: ${this.#destroyed} }`;
  }
}

export function assertSessionSubkey(
  value: unknown,
): asserts value is DerivedSubkeyHandle<"session"> {
  assertSubkeyPurpose(value, "session");
}

export function assertExportSubkey(
  value: unknown,
): asserts value is DerivedSubkeyHandle<"export"> {
  assertSubkeyPurpose(value, "export");
}

export async function deriveLoginKeyHandles(
  material: LoginKeyInput,
): Promise<LoginKeyHandles> {
  const normalized = normalizeLoginKeyMaterial(material);
  let sessionKey: Uint8Array | undefined;
  let exportKey: Uint8Array | undefined;

  try {
    sessionKey = toKeyBytes(normalized.sessionKey, "sessionKey");
    exportKey = toKeyBytes(normalized.exportKey, "exportKey");
    const clientIdentity = encodeIdentity(normalized.clientIdentity, "clientIdentity");
    const serverIdentity = encodeIdentity(normalized.serverIdentity, "serverIdentity");
    const transcriptHash = toTranscriptHash(normalized.transcriptHash);

    if (sessionKey.byteLength === 0 || exportKey.byteLength === 0) {
      throw new TypeError("OPAQUE session and export keys must not be empty");
    }
    if (transcriptHash.byteLength === 0) {
      throw new TypeError("The login transcript hash must not be empty");
    }
    if (constantTimeEqual(sessionKey, exportKey)) {
      throw new TypeError("The session key and export key must be different");
    }

    const sessionIkm = await crypto.subtle.importKey(
      "raw",
      sessionKey,
      "HKDF",
      false,
      ["deriveKey"],
    );
    const exportIkm = await crypto.subtle.importKey(
      "raw",
      exportKey,
      "HKDF",
      false,
      ["deriveKey"],
    );

    const salt = transcriptHash.slice();
    const [session, exported] = await Promise.all([
      deriveAesKey(
        sessionIkm,
        salt,
        buildSubkeyInfo("session", clientIdentity, serverIdentity, transcriptHash),
      ),
      deriveAesKey(
        exportIkm,
        salt,
        buildSubkeyInfo("export", clientIdentity, serverIdentity, transcriptHash),
      ),
    ]);

    return Object.freeze({
      session: Object.freeze(
        new DerivedSubkeyHandle(HANDLE_BRAND, "session", session),
      ),
      export: Object.freeze(
        new DerivedSubkeyHandle(HANDLE_BRAND, "export", exported),
      ),
    });
  } finally {
    if (sessionKey !== undefined) {
      wipeBytes(sessionKey);
    }
    if (exportKey !== undefined) {
      wipeBytes(exportKey);
    }
  }
}

export const createLoginKeyHandles = deriveLoginKeyHandles;
export const deriveOpaqueKeyHandles = deriveLoginKeyHandles;

function assertSubkeyPurpose(
  value: unknown,
  expected: OpaqueKeyPurpose,
): asserts value is DerivedSubkeyHandle<OpaqueKeyPurpose> {
  if (!(value instanceof DerivedSubkeyHandle) || value.purpose !== expected) {
    throw new TypeError(`Expected an OPAQUE ${expected} subkey handle`);
  }
}

function normalizeLoginKeyMaterial(material: LoginKeyInput): LoginKeyMaterial {
  if (material === null || typeof material !== "object") {
    throw new TypeError("Login key material must be an object");
  }
  const input = material as unknown as Record<string, unknown>;
  const sessionKey = input.sessionKey ?? input.session_key;
  const exportKey = input.exportKey ?? input.export_key;
  const clientIdentity = input.clientIdentity ?? input.client_identity;
  const serverIdentity = input.serverIdentity ?? input.server_identity;
  const transcriptHash = input.transcriptHash ?? input.transcript_hash;

  if (sessionKey === undefined || exportKey === undefined) {
    throw new TypeError("sessionKey/session_key and exportKey/export_key are required");
  }
  if (clientIdentity === undefined || serverIdentity === undefined) {
    throw new TypeError("Client and server identities are required");
  }
  if (transcriptHash === undefined) {
    throw new TypeError("transcriptHash/transcript_hash is required");
  }

  return {
    sessionKey: sessionKey as KeyBytes,
    exportKey: exportKey as KeyBytes,
    clientIdentity: clientIdentity as Identity,
    serverIdentity: serverIdentity as Identity,
    transcriptHash: transcriptHash as Uint8Array,
  };
}

async function deriveAesKey(
  ikm: CryptoKey,
  salt: Uint8Array,
  info: Uint8Array,
): Promise<CryptoKey> {
  return crypto.subtle.deriveKey(
    {
      name: "HKDF",
      hash: "SHA-256",
      salt,
      info,
    },
    ikm,
    {
      name: "AES-GCM",
      length: 256,
    },
    false,
    ["encrypt", "decrypt"],
  );
}

function buildSubkeyInfo(
  purpose: OpaqueKeyPurpose,
  clientIdentity: Uint8Array,
  serverIdentity: Uint8Array,
  transcriptHash: Uint8Array,
): Uint8Array {
  return concatBytes([
    lengthPrefixed(encoder.encode(SUBKEY_CONTEXT_DOMAIN)),
    lengthPrefixed(encoder.encode(SUBKEY_LABELS[purpose])),
    lengthPrefixed(clientIdentity),
    lengthPrefixed(serverIdentity),
    lengthPrefixed(transcriptHash),
  ]);
}

function lengthPrefixed(value: Uint8Array): Uint8Array {
  if (value.byteLength > 0xffffffff) {
    throw new RangeError("HKDF context field is too large");
  }
  const result = new Uint8Array(4 + value.byteLength);
  const view = new DataView(result.buffer);
  view.setUint32(0, value.byteLength, false);
  result.set(value, 4);
  return result;
}

function concatBytes(parts: readonly Uint8Array[]): Uint8Array {
  let length = 0;
  for (const part of parts) {
    length += part.byteLength;
  }
  const result = new Uint8Array(length);
  let offset = 0;
  for (const part of parts) {
    result.set(part, offset);
    offset += part.byteLength;
  }
  return result;
}

function encodeIdentity(value: Identity, name: string): Uint8Array {
  if (typeof value === "string") {
    return encoder.encode(value);
  }
  if (value instanceof Uint8Array) {
    return value.slice();
  }
  throw new TypeError(`${name} must be a string or Uint8Array`);
}

function toKeyBytes(value: KeyBytes, name: string): Uint8Array {
  if (value instanceof Uint8Array) {
    return value;
  }
  if (value instanceof ArrayBuffer) {
    return new Uint8Array(value);
  }
  throw new TypeError(`${name} must be a Uint8Array or ArrayBuffer`);
}

function toTranscriptHash(value: Uint8Array): Uint8Array {
  if (!(value instanceof Uint8Array)) {
    throw new TypeError("transcriptHash must be a Uint8Array");
  }
  return value.slice();
}

function constantTimeEqual(left: Uint8Array, right: Uint8Array): boolean {
  const length = Math.max(left.byteLength, right.byteLength);
  let difference = left.byteLength ^ right.byteLength;
  for (let index = 0; index < length; index += 1) {
    difference |= (left[index % left.byteLength] ?? 0) ^ (right[index % right.byteLength] ?? 0);
  }
  return difference === 0;
}

function wipeBytes(value: KeyBytes): void {
  if (value instanceof ArrayBuffer) {
    new Uint8Array(value).fill(0);
    return;
  }
  value.fill(0);
}
