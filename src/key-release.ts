import {
  createPublicKey,
  KeyObject,
  publicEncrypt,
} from "node:crypto";
import {
  KeyWrappingError,
  RuntimeDataBindingError,
  TokenVerificationError,
} from "./errors.ts";
import { assertPolicySatisfied } from "./policy.ts";
import {
  extractWrappingKey,
  verifyRuntimeDataBinding,
  type HashAlgorithm,
} from "./runtime-data.ts";
import type {
  AttestationToken,
  KeyReleaseOptions,
  KeyStore,
  PolicyNode,
  ReleaseRequest,
  ReleaseResult,
  TokenVerifier,
} from "./types.ts";

const OAEP_LABELS: Record<string, string> = {
  "RSA-OAEP": "SHA-1",
  "RSA-OAEP-256": "SHA-256",
  "RSA-OAEP-384": "SHA-384",
  "RSA-OAEP-512": "SHA-512",
};

function isPem(key: string): boolean {
  return key.includes("-----BEGIN");
}

/**
 * Import a wrapping key given either as PEM (SPKI/PKCS#1) or base64 DER.
 * Only RSA public keys are accepted for RSA-OAEP wrapping.
 */
export function importWrappingPublicKey(
  encoded: string,
  wrappingAlgorithm: ReleaseResult["wrappingAlgorithm"],
): KeyObject {
  let key: KeyObject;
  try {
    if (isPem(encoded)) {
      key = createPublicKey(encoded);
    } else {
      key = createPublicKey({
        key: Buffer.from(encoded.trim(), "base64"),
        format: "der",
        type: "spki",
      });
    }
  } catch (cause) {
    throw new RuntimeDataBindingError(
      "wrapping public key in runtime-data is not a parseable public key",
      { cause },
    );
  }

  if (key.asymmetricKeyType !== "rsa") {
    throw new RuntimeDataBindingError(
      `wrapping algorithm ${wrappingAlgorithm} requires an RSA public key, got ${key.asymmetricKeyType}`,
    );
  }
  return key;
}

/**
 * Wrap key material with the attested public key. There is intentionally no
 * parameter to pass in a key from anywhere else: callers cannot accidentally
 * (or maliciously) wrap to a host-supplied key.
 */
export function wrapKey(
  keyMaterial: ArrayBuffer,
  wrappingPublicKey: KeyObject,
  wrappingAlgorithm: ReleaseResult["wrappingAlgorithm"] = "RSA-OAEP-256",
): Buffer {
  if (wrappingPublicKey.type !== "public") {
    throw new KeyWrappingError("wrapping key must be a public key object");
  }
  const requestedHash = OAEP_LABELS[wrappingAlgorithm];
  try {
    return publicEncrypt(
      {
        key: wrappingPublicKey,
        padding: 4, // RSA_PKCS1_OAEP_PADDING
        oaepHash: requestedHash,
      },
      Buffer.from(keyMaterial),
    );
  } catch (cause) {
    throw new KeyWrappingError(
      `failed to wrap key with ${wrappingAlgorithm}: key material likely exceeds the RSA-OAEP message limit`,
      { cause },
    );
  }
}

export interface KeyReleaseServiceDeps {
  readonly verifyToken: TokenVerifier;
  readonly keyStore: KeyStore;
}

/**
 * Entry point used by the key-release interface.
 *
 * Pipeline:
 *  1. injected verifier authenticates the token (signature, expiry, issuer…)
 *  2. nested allOf/anyOf claim policy is evaluated with strict typing
 *  3. runtime-data hash is checked against the report's REPORT_DATA
 *  4. the wrapping key is taken from that attested runtime-data only
 *  5. the stored key is wrapped exclusively to that public key
 */
export class KeyReleaseService {
  private readonly policy: PolicyNode;
  private readonly deps: KeyReleaseServiceDeps;
  private readonly options: KeyReleaseOptions;

  constructor(
    policy: PolicyNode,
    deps: KeyReleaseServiceDeps,
    options: KeyReleaseOptions = {},
  ) {
    this.policy = policy;
    this.deps = deps;
    this.options = options;
  }

  async releaseKey(request: ReleaseRequest): Promise<ReleaseResult> {
    let attested: AttestationToken;
    try {
      attested = await this.deps.verifyToken(request.token);
    } catch (cause) {
      throw new TokenVerificationError("attestation token verification failed", {
        cause,
      });
    }

    // Throws PolicyNotSatisfiedError (with trace) on any mismatch.
    assertPolicySatisfied(this.policy, attested.claims);

    const binding = verifyRuntimeDataBinding(attested.claims, {
      runtimeDataPath: this.options.runtimeDataPath,
      reportDataPaths: this.options.reportDataPaths,
      runtimeDataHash: this.options.runtimeDataHash ?? "auto",
    });

    const { publicKey } = extractWrappingKey(
      binding.runtimeData,
      this.options.publicKeyPaths,
    );

    const wrappingAlgorithm =
      this.options.wrappingAlgorithm ?? "RSA-OAEP-256";
    const wrappingPublicKey = importWrappingPublicKey(
      publicKey,
      wrappingAlgorithm,
    );

    const record = await this.deps.keyStore.get(request.keyId);
    if (!record) {
      throw new KeyWrappingError(`no key found for keyId "${request.keyId}"`);
    }

    const wrappedKey = wrapKey(
      record.keyMaterial,
      wrappingPublicKey,
      wrappingAlgorithm,
    );

    return {
      keyId: request.keyId,
      wrappedKey,
      wrappingAlgorithm,
      wrappedKeyHashAlgorithm: binding.hashAlgorithm,
    };
  }
}

export function wrapWithRuntimeDataKey(
  keyMaterial: ArrayBuffer,
  runtimeData: import("./types.ts").RuntimeData,
  options: {
    publicKeyPaths?: ReadonlyArray<string>;
    wrappingAlgorithm?: ReleaseResult["wrappingAlgorithm"];
  } = {},
): { wrappedKey: Buffer; wrappingAlgorithm: ReleaseResult["wrappingAlgorithm"] } {
  const wrappingAlgorithm = options.wrappingAlgorithm ?? "RSA-OAEP-256";
  const { publicKey } = extractWrappingKey(
    runtimeData,
    options.publicKeyPaths,
  );
  const key = importWrappingPublicKey(publicKey, wrappingAlgorithm);
  return {
    wrappedKey: wrapKey(keyMaterial, key, wrappingAlgorithm),
    wrappingAlgorithm,
  };
}
export type { HashAlgorithm };
