/**
 * JSON-shaped token claims. Only values representable in JSON can appear in a
 * JWT/SWT attestation token, so this union is deliberately closed.
 */
export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonObject
  | JsonValue[];

export interface JsonObject {
  [key: string]: JsonValue;
}

/** A verified attestation token: claims plus the original bearer string. */
export interface AttestationToken {
  readonly claims: JsonObject;
  readonly rawToken: string;
}

/**
 * Signature verification is injected by the caller. The agent never owns the
 * attestation authority key. The verifier must validate signature, expiry,
 * issuer/audience and return the parsed claims, or throw.
 */
export type TokenVerifier = (token: string) => Promise<AttestationToken>;

/**
 * A leaf condition against one claim path (dot-separated, numeric segments
 * index arrays). At least one operator is required; when several are given
 * every one of them must hold. All comparisons are strictly typed:
 * the string "false" never equals the boolean false.
 */
export interface ClaimRule {
  readonly claim: string;
  readonly equals?: JsonValue;
  readonly notEquals?: JsonValue;
  readonly in?: ReadonlyArray<JsonValue>;
  readonly notIn?: ReadonlyArray<JsonValue>;
  readonly exists?: boolean;
  readonly gt?: number;
  readonly gte?: number;
  readonly lt?: number;
  readonly lte?: number;
}

export type PolicyNode =
  | { readonly allOf: ReadonlyArray<PolicyNode> }
  | { readonly anyOf: ReadonlyArray<PolicyNode> }
  | ClaimRule;

export type RuleResult =
  | { kind: "rule"; rule: ClaimRule; satisfied: boolean; reason: string }
  | {
      kind: "allOf" | "anyOf";
      satisfied: boolean;
      reason: string;
      children: TraceEntry[];
    };

export type TraceEntry = RuleResult;

export interface PolicyEvaluation {
  readonly satisfied: boolean;
  readonly trace: TraceEntry[];
}

/**
 * The runtime-data object the attester bound to the report. The wrapping key
 * is carried inside it (e.g. CoCo KBS `public` / Intel Trust Authority
 * `public-key` conventions).
 */
export type RuntimeData = JsonObject;

export interface ReleaseRequest {
  readonly token: string;
  readonly keyId: string;
}

export interface ReleaseResult {
  readonly keyId: string;
  /** The key material encrypted to the runtime-data public key. */
  readonly wrappedKey: Uint8Array;
  readonly wrappingAlgorithm:
    | "RSA-OAEP"
    | "RSA-OAEP-256"
    | "RSA-OAEP-384"
    | "RSA-OAEP-512";
  readonly wrappedKeyHashAlgorithm: "SHA-256" | "SHA-384";
}

export interface KeyRecord {
  readonly keyId: string;
  /** Raw symmetric/asymmetric key material held by the agent. */
  readonly keyMaterial: ArrayBuffer;
}

export type KeyStore = {
  get(keyId: string): Promise<KeyRecord | undefined>;
};

export interface KeyReleaseOptions {
  /** Dot paths tried in order when locating the wrapping key. */
  readonly publicKeyPaths?: ReadonlyArray<string>;
  /**
   * Report-data claim paths tried in order (CoCo attesters vary between
   * `report_data` / `report-data`, top level or under `tdx`/`sgx`).
   */
  readonly reportDataPaths?: ReadonlyArray<string>;
  /** Claim path carrying the runtime-data object. */
  readonly runtimeDataPath?: string;
  /**
   * Hash used to bind runtime-data to REPORT_DATA. Defaults to SHA-384
   * (Intel TDX quote); SHA-256 covers SGX. "auto" picks the digest by the
   * decoded report-data length.
   */
  readonly runtimeDataHash?: "SHA-256" | "SHA-384" | "auto";
  readonly wrappingAlgorithm?: ReleaseResult["wrappingAlgorithm"];
}
