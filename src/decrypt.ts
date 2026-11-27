import {
  createDecipheriv,
  createHash,
  createHmac,
  createPrivateKey,
  createPublicKey,
  diffieHellman,
  pbkdf2Sync,
  privateDecrypt,
  timingSafeEqual,
  constants as cryptoConstants,
  type KeyObject,
} from "node:crypto";
import { JweSecurityError } from "./errors.ts";
import { boundedInflate } from "./inflate.ts";
import {
  validateJwe,
  type ValidatedJwe,
} from "./jwe.ts";
import {
  ECDH_ES_ALGORITHMS,
  PBES2_ALGORITHMS,
  type JwePolicy,
} from "./policy.ts";
import type { StrictJsonObject } from "./json.ts";

export type JweKeyBytes = Buffer | Uint8Array | string | KeyObject;
export type JwePrivateKeyInput = Buffer | Uint8Array | string | KeyObject;

export interface JweKeyMaterial {
  /** CEK for alg="dir". */
  readonly cek?: JweKeyBytes;
  /** KEK for A128KW/A192KW/A256KW. */
  readonly kek?: JweKeyBytes;
  /** Password for the PBES2 family. */
  readonly pbes2Password?: JweKeyBytes;
  /** Alias for pbes2Password. */
  readonly password?: JweKeyBytes;
  /** RSA or EC private key for RSA-OAEP / ECDH-ES. */
  readonly privateKey?: JwePrivateKeyInput;
  /** Context-dependent shorthand; see decryptJwe(). */
  readonly key?: JweKeyBytes;
}

export interface JweDecryptionResult {
  readonly plaintext: Buffer;
  readonly header: StrictJsonObject;
  readonly algorithm: string;
  readonly encryption: string;
  readonly compressed: boolean;
  readonly protectedHeaderBytes: Buffer;
  readonly aad: Buffer;
}

interface ContentEncryptionParameters {
  readonly kind: "gcm" | "cbc-hs";
  readonly cekBytes: number;
  readonly ivBytes: number;
  readonly tagBytes: number;
  readonly cipher?: string;
  readonly macHash?: "sha256" | "sha384" | "sha512";
  readonly macKeyBytes?: number;
}

const CONTENT_ENCRYPTION: Record<string, ContentEncryptionParameters> =
  Object.freeze({
    A128GCM: {
      kind: "gcm",
      cekBytes: 16,
      ivBytes: 12,
      tagBytes: 16,
      cipher: "aes-128-gcm",
    },
    A192GCM: {
      kind: "gcm",
      cekBytes: 24,
      ivBytes: 12,
      tagBytes: 16,
      cipher: "aes-192-gcm",
    },
    A256GCM: {
      kind: "gcm",
      cekBytes: 32,
      ivBytes: 12,
      tagBytes: 16,
      cipher: "aes-256-gcm",
    },
    "A128CBC-HS256": {
      kind: "cbc-hs",
      cekBytes: 32,
      ivBytes: 16,
      tagBytes: 16,
      cipher: "aes-128-cbc",
      macHash: "sha256",
      macKeyBytes: 16,
    },
    "A192CBC-HS384": {
      kind: "cbc-hs",
      cekBytes: 48,
      ivBytes: 16,
      tagBytes: 24,
      cipher: "aes-192-cbc",
      macHash: "sha384",
      macKeyBytes: 24,
    },
    "A256CBC-HS512": {
      kind: "cbc-hs",
      cekBytes: 64,
      ivBytes: 16,
      tagBytes: 32,
      cipher: "aes-256-cbc",
      macHash: "sha512",
      macKeyBytes: 32,
    },
  });

const AES_KW_BYTES: Record<string, number> = Object.freeze({
  A128KW: 16,
  A192KW: 24,
  A256KW: 32,
});

const PBES2_PARAMETERS: Record<
  string,
  { readonly kekBytes: number; readonly hash: "sha256" | "sha384" | "sha512" }
> = Object.freeze({
  "PBES2-HS256+A128KW": { kekBytes: 16, hash: "sha256" },
  "PBES2-HS384+A192KW": { kekBytes: 24, hash: "sha384" },
  "PBES2-HS512+A256KW": { kekBytes: 32, hash: "sha512" },
});

const ECDH_KW_BYTES: Record<string, number> = Object.freeze({
  "ECDH-ES+A128KW": 16,
  "ECDH-ES+A192KW": 24,
  "ECDH-ES+A256KW": 32,
});

const AES_KW_IV = Buffer.from("a6a6a6a6a6a6a6a6", "hex");

function keyError(
  code: ConstructorParameters<typeof JweSecurityError>[0],
  message: string,
  details?: Record<string, unknown>,
): JweSecurityError {
  return new JweSecurityError(code, message, details);
}

function isKeyObject(value: unknown): value is KeyObject {
  return (
    typeof value === "object" &&
    value !== null &&
    typeof (value as KeyObject).export === "function" &&
    typeof (value as KeyObject).type === "string"
  );
}

function isSimpleKeyMaterial(value: unknown): value is JweKeyBytes {
  return (
    typeof value === "string" ||
    Buffer.isBuffer(value) ||
    value instanceof Uint8Array ||
    isKeyObject(value)
  );
}

function keyBytes(value: unknown, name: string): Buffer {
  if (value === undefined) {
    throw keyError("JWE_KEY_MATERIAL_MISSING", `${name} is required`);
  }
  if (Buffer.isBuffer(value)) {
    return value;
  }
  if (value instanceof Uint8Array) {
    return Buffer.from(value);
  }
  if (typeof value === "string") {
    return Buffer.from(value, "utf8");
  }
  if (isKeyObject(value) && value.type === "secret") {
    return Buffer.from(value.export());
  }
  throw keyError(
    "JWE_KEY_LENGTH_INVALID",
    `${name} must be raw bytes, a UTF-8 string, or a symmetric KeyObject`,
  );
}

function exactKey(value: unknown, name: string, bytes: number): Buffer {
  const key = keyBytes(value, name);
  if (key.length !== bytes) {
    throw keyError(
      "JWE_KEY_LENGTH_INVALID",
      `${name} must contain exactly ${bytes} bytes`,
      { actualBytes: key.length, expectedBytes: bytes },
    );
  }
  return key;
}

function privateKey(value: unknown, name: string): KeyObject {
  if (value === undefined) {
    throw keyError("JWE_KEY_MATERIAL_MISSING", `${name} is required`);
  }
  if (isKeyObject(value)) {
    if (value.type !== "private") {
      throw keyError(
        "JWE_KEY_MATERIAL_MISSING",
        `${name} must be a private KeyObject`,
      );
    }
    return value;
  }

  try {
    const key = createPrivateKey(
      Buffer.isBuffer(value) || typeof value === "string"
        ? value
        : Buffer.from(value as Uint8Array),
    );
    if (key.type !== "private") {
      throw new Error("not private");
    }
    return key;
  } catch {
    throw keyError(
      "JWE_KEY_MATERIAL_MISSING",
      `${name} is not a usable private key`,
    );
  }
}

function simpleKeyMaterial(
  algorithm: string,
  value: JweKeyBytes,
): JweKeyMaterial {
  if (algorithm === "dir") {
    return { cek: value };
  }
  if (algorithm in AES_KW_BYTES) {
    return { kek: value };
  }
  if (PBES2_ALGORITHMS.has(algorithm)) {
    return { pbes2Password: value };
  }
  return { privateKey: value };
}

function normalizedKeyMaterial(
  algorithm: string,
  input: JweKeyMaterial | JweKeyBytes,
): JweKeyMaterial {
  if (isSimpleKeyMaterial(input)) {
    return simpleKeyMaterial(algorithm, input);
  }
  if (input === null || typeof input !== "object" || Array.isArray(input)) {
    throw keyError(
      "JWE_KEY_MATERIAL_MISSING",
      "key material must be an object or a context-appropriate key",
    );
  }
  return input;
}

function contentParameters(encryption: string): ContentEncryptionParameters {
  const parameters = CONTENT_ENCRYPTION[encryption];
  if (parameters === undefined) {
    throw keyError(
      "JWE_KEY_MANAGEMENT_UNSUPPORTED",
      `content encryption ${JSON.stringify(encryption)} is not implemented`,
    );
  }
  return parameters;
}

function aesKeyUnwrap(
  kek: Buffer,
  wrapped: Buffer,
  expectedBytes: number,
): Buffer {
  if (wrapped.length !== expectedBytes + 8) {
    throw keyError(
      "JWE_ENCRYPTED_KEY_INVALID",
      "AES-KW encrypted key has an unexpected length",
      { actualBytes: wrapped.length, expectedBytes: expectedBytes + 8 },
    );
  }

  try {
    const decipher = createDecipheriv(
      `aes${kek.length * 8}-wrap`,
      kek,
      AES_KW_IV,
    );
    const cek = Buffer.concat([decipher.update(wrapped), decipher.final()]);
    if (cek.length !== expectedBytes) {
      throw new Error("unexpected unwrapped key length");
    }
    return cek;
  } catch {
    throw keyError(
      "JWE_KEY_UNWRAP_FAILED",
      "AES key unwrap failed",
    );
  }
}

function validateEcdhPrivateKey(key: KeyObject): KeyObject {
  if (key.asymmetricKeyType !== "ec") {
    throw keyError(
      "JWE_KEY_MATERIAL_MISSING",
      "ECDH-ES requires an EC private key",
    );
  }

  try {
    const jwk = key.export({ format: "jwk" }) as {
      readonly kty?: unknown;
      readonly crv?: unknown;
    };
    if (jwk.kty !== "EC" || jwk.crv !== "P-256") {
      throw new Error("wrong curve");
    }
  } catch {
    throw keyError(
      "JWE_KEY_MATERIAL_MISSING",
      "ECDH-ES static private key must use P-256",
    );
  }
  return key;
}

function ecdhSecret(
  privateKeyValue: KeyObject,
  epk: StrictJsonObject,
): Buffer {
  let publicKey: KeyObject;
  try {
    publicKey = createPublicKey({
      key: {
        kty: "EC",
        crv: "P-256",
        x: epk.x as string,
        y: epk.y as string,
      },
      format: "jwk",
    });
  } catch {
    throw keyError(
      "JWE_ECDH_EPK_INVALID",
      "validated epk could not be imported as a public key",
    );
  }

  try {
    const secret = diffieHellman({
      privateKey: privateKeyValue,
      publicKey,
    });
    if (secret.length !== 32) {
      throw new Error("unexpected P-256 shared secret length");
    }
    return secret;
  } catch {
    throw keyError(
      "JWE_KEY_UNWRAP_FAILED",
      "ECDH shared-secret computation failed",
    );
  }
}

export function deriveConcatKdf(
  sharedSecret: Buffer | Uint8Array,
  algorithmId: string,
  keyLengthBits: number,
  partyUInfo?: Buffer | Uint8Array,
  partyVInfo?: Buffer | Uint8Array,
): Buffer {
  if (
    !Number.isSafeInteger(keyLengthBits) ||
    keyLengthBits <= 0 ||
    keyLengthBits > 0xffff_ffff
  ) {
    throw keyError(
      "JWE_KEY_LENGTH_INVALID",
      "Concat KDF keyLengthBits must be a positive safe integer",
    );
  }
  if (typeof algorithmId !== "string" || algorithmId.length === 0) {
    throw keyError(
      "JWE_KEY_LENGTH_INVALID",
      "Concat KDF AlgorithmID must be a non-empty string",
    );
  }

  const secret = Buffer.from(sharedSecret);
  const apu = partyUInfo === undefined ? Buffer.alloc(0) : Buffer.from(partyUInfo);
  const apv = partyVInfo === undefined ? Buffer.alloc(0) : Buffer.from(partyVInfo);
  const keyBytes = Math.ceil(keyLengthBits / 8);
  const repetitions = Math.ceil(keyBytes / 32);
  if (repetitions > 0xff_ffff_ff) {
    throw keyError("JWE_KEY_LENGTH_INVALID", "Concat KDF output is too large");
  }

  const algorithmIdBytes = Buffer.from(algorithmId, "utf8");
  const suppPubInfo = Buffer.alloc(4);
  suppPubInfo.writeUInt32BE(keyLengthBits, 0);

  const output: Buffer[] = [];
  for (let counter = 1; counter <= repetitions; counter += 1) {
    const counterBytes = Buffer.alloc(4);
    counterBytes.writeUInt32BE(counter, 0);
    const digest = createHash("sha256")
      .update(secret)
      .update(counterBytes)
      .update(algorithmIdBytes)
      .update(apu)
      .update(apv)
      .update(suppPubInfo)
      .digest();
    output.push(digest);
  }

  return Buffer.concat(output, repetitions * 32).subarray(0, keyBytes);
}

function unwrapOrDeriveCek(
  admitted: ValidatedJwe,
  material: JweKeyMaterial,
  encryptionParameters: ContentEncryptionParameters,
): Buffer {
  const { algorithm } = admitted;

  if (algorithm === "dir") {
    if (admitted.encryptedKey.length !== 0) {
      throw keyError(
        "JWE_ENCRYPTED_KEY_INVALID",
        'alg="dir" requires an empty encrypted-key segment',
      );
    }
    return exactKey(
      material.cek ?? material.key,
      "CEK",
      encryptionParameters.cekBytes,
    );
  }

  if (algorithm in AES_KW_BYTES) {
    const kekBytes = AES_KW_BYTES[algorithm]!;
    const kek = exactKey(material.kek ?? material.key, "KEK", kekBytes);
    return aesKeyUnwrap(
      kek,
      admitted.encryptedKey,
      encryptionParameters.cekBytes,
    );
  }

  if (PBES2_ALGORITHMS.has(algorithm)) {
    const parameters = PBES2_PARAMETERS[algorithm]!;
    const password = keyBytes(
      material.pbes2Password ?? material.password ?? material.key,
      "PBES2 password",
    );
    if (admitted.pbes2 === undefined) {
      throw keyError(
        "JWE_PBES2_P2C_MISSING",
        "validated PBES2 metadata is unavailable",
      );
    }
    const salt = Buffer.concat([
      Buffer.from(algorithm, "utf8"),
      admitted.pbes2.salt,
    ]);

    let kek: Buffer;
    try {
      kek = pbkdf2Sync(
        password,
        salt,
        admitted.pbes2.iterations,
        parameters.kekBytes,
        parameters.hash,
      );
    } catch {
      throw keyError(
        "JWE_KEY_UNWRAP_FAILED",
        "PBES2 key derivation failed",
      );
    }
    return aesKeyUnwrap(
      kek,
      admitted.encryptedKey,
      encryptionParameters.cekBytes,
    );
  }

  if (algorithm === "RSA-OAEP" || algorithm === "RSA-OAEP-256") {
    if (admitted.encryptedKey.length === 0) {
      throw keyError(
        "JWE_ENCRYPTED_KEY_INVALID",
        "RSA key management requires a non-empty encrypted key",
      );
    }
    const key = privateKey(
      material.privateKey ?? material.key,
      "RSA private key",
    );
    if (key.asymmetricKeyType !== "rsa") {
      throw keyError(
        "JWE_KEY_MATERIAL_MISSING",
        "RSA-OAEP requires an RSA private key",
      );
    }

    let cek: Buffer;
    try {
      cek = privateDecrypt(
        {
          key,
          padding: cryptoConstants.RSA_PKCS1_OAEP_PADDING,
          oaepHash: algorithm === "RSA-OAEP" ? "sha1" : "sha256",
        },
        admitted.encryptedKey,
      );
    } catch {
      throw keyError("JWE_KEY_UNWRAP_FAILED", "RSA-OAEP key unwrap failed");
    }
    if (cek.length !== encryptionParameters.cekBytes) {
      throw keyError(
        "JWE_KEY_LENGTH_INVALID",
        "unwrapped CEK has an unexpected length",
        {
          actualBytes: cek.length,
          expectedBytes: encryptionParameters.cekBytes,
        },
      );
    }
    return cek;
  }

  if (ECDH_ES_ALGORITHMS.has(algorithm)) {
    if (admitted.ecdhEpk === undefined) {
      throw keyError(
        "JWE_ECDH_EPK_MISSING",
        "validated ECDH ephemeral public key is unavailable",
      );
    }
    if (algorithm === "ECDH-ES" && admitted.encryptedKey.length !== 0) {
      throw keyError(
        "JWE_ENCRYPTED_KEY_INVALID",
        "direct ECDH-ES requires an empty encrypted-key segment",
      );
    }
    const key = validateEcdhPrivateKey(
      privateKey(material.privateKey ?? material.key, "ECDH private key"),
    );
    const secret = ecdhSecret(key, admitted.ecdhEpk.jwk);

    if (algorithm === "ECDH-ES") {
      return deriveConcatKdf(
        secret,
        admitted.encryption,
        encryptionParameters.cekBytes * 8,
        admitted.apu,
        admitted.apv,
      );
    }

    const kekBytes = ECDH_KW_BYTES[algorithm]!;
    const kek = deriveConcatKdf(
      secret,
      algorithm,
      kekBytes * 8,
      admitted.apu,
      admitted.apv,
    );
    return aesKeyUnwrap(
      kek,
      admitted.encryptedKey,
      encryptionParameters.cekBytes,
    );
  }

  throw keyError(
    "JWE_KEY_MANAGEMENT_UNSUPPORTED",
    `key management algorithm ${JSON.stringify(algorithm)} is not implemented`,
  );
}

function decryptWithAead(
  admitted: ValidatedJwe,
  cek: Buffer,
  encryptionParameters: ContentEncryptionParameters,
): Buffer {
  if (admitted.iv.length !== encryptionParameters.ivBytes) {
    throw keyError(
      "JWE_IV_INVALID",
      "initialization vector length does not match enc",
      {
        actualBytes: admitted.iv.length,
        expectedBytes: encryptionParameters.ivBytes,
      },
    );
  }
  if (admitted.tag.length !== encryptionParameters.tagBytes) {
    throw keyError(
      "JWE_TAG_INVALID",
      "authentication tag length does not match enc",
      {
        actualBytes: admitted.tag.length,
        expectedBytes: encryptionParameters.tagBytes,
      },
    );
  }

  if (encryptionParameters.kind === "gcm") {
    try {
      const decipher = createDecipheriv(
        encryptionParameters.cipher!,
        cek,
        admitted.iv,
        { authTagLength: admitted.tag.length },
      );
      decipher.setAAD(admitted.aad);
      decipher.setAuthTag(admitted.tag);
      return Buffer.concat([
        decipher.update(admitted.ciphertext),
        decipher.final(),
      ]);
    } catch {
      throw keyError(
        "JWE_AUTH_TAG_INVALID",
        "JWE authentication failed",
      );
    }
  }

  const macKey = cek.subarray(0, encryptionParameters.macKeyBytes);
  const contentKey = cek.subarray(encryptionParameters.macKeyBytes);
  const expectedTag = createHmac(encryptionParameters.macHash!, macKey)
    .update(admitted.aad)
    .update(admitted.iv)
    .update(admitted.ciphertext)
    .digest()
    .subarray(0, encryptionParameters.tagBytes);

  if (!timingSafeEqual(expectedTag, admitted.tag)) {
    throw keyError(
      "JWE_AUTH_TAG_INVALID",
      "JWE authentication failed",
    );
  }

  try {
    const decipher = createDecipheriv(
      encryptionParameters.cipher!,
      contentKey,
      admitted.iv,
    );
    return Buffer.concat([
      decipher.update(admitted.ciphertext),
      decipher.final(),
    ]);
  } catch {
    throw keyError(
      "JWE_DECRYPTION_FAILED",
      "authenticated content could not be decrypted",
    );
  }
}

/**
 * Decrypts a compact JWE after the admission checks have passed. Authentication
 * is completed before zip="DEF" decompression is started.
 */
export async function decryptJwe(
  token: string,
  keyMaterialInput: JweKeyMaterial | JweKeyBytes = {},
  policyInput: JwePolicy = {},
): Promise<JweDecryptionResult> {
  const admitted = validateJwe(token, policyInput);
  const material = normalizedKeyMaterial(admitted.algorithm, keyMaterialInput);
  const encryptionParameters = contentParameters(admitted.encryption);
  const cek = unwrapOrDeriveCek(admitted, material, encryptionParameters);
  const decrypted = decryptWithAead(admitted, cek, encryptionParameters);

  let plaintext: Buffer;
  if (admitted.compression === "DEF") {
    plaintext = await boundedInflate(decrypted, {
      maxPlaintextBytes: admitted.policy.maxPlaintextBytes,
      maxRatio: admitted.policy.maxRatio,
    });
  } else {
    if (decrypted.length > admitted.policy.maxPlaintextBytes) {
      throw keyError(
        "JWE_PLAINTEXT_TOO_LARGE",
        "plaintext exceeds maxPlaintextBytes",
        {
          actualBytes: decrypted.length,
          maxPlaintextBytes: admitted.policy.maxPlaintextBytes,
        },
      );
    }
    plaintext = decrypted;
  }

  return Object.freeze({
    plaintext,
    header: admitted.protectedHeader,
    algorithm: admitted.algorithm,
    encryption: admitted.encryption,
    compressed: admitted.compression === "DEF",
    protectedHeaderBytes: admitted.protectedHeaderBytes,
    aad: admitted.aad,
  });
}

export { decryptJwe as unsealJwe };
