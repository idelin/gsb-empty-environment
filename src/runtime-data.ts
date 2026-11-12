import { createHash, timingSafeEqual } from "node:crypto";
import { RuntimeDataBindingError } from "./errors.ts";
import { resolveClaim } from "./policy.ts";
import type { JsonObject, JsonValue, RuntimeData } from "./types.ts";

export type HashAlgorithm = "SHA-256" | "SHA-384";

/**
 * Deterministic JSON serialization: object keys sorted recursively, no
 * whitespace, so the attester and the agent hash identical bytes regardless
 * of key insertion order.
 */
export function canonicalize(value: JsonValue): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => canonicalize(item)).join(",")}]`;
  }
  const entries = Object.keys(value)
    .sort()
    .map((key) => `${JSON.stringify(key)}:${canonicalize(value[key])}`);
  return `{${entries.join(",")}}`;
}

export function hashRuntimeData(
  runtimeData: RuntimeData,
  algorithm: HashAlgorithm = "SHA-384",
): Buffer {
  return createHash(algorithm)
    .update(canonicalize(runtimeData), "utf8")
    .digest();
}

/** Decode REPORT_DATA given as hex or base64; reject malformed encodings. */
export function decodeReportData(encoded: string): Buffer {
  const trimmed = encoded.trim();
  if (/^[0-9a-fA-F]+$/.test(trimmed) && trimmed.length % 2 === 0) {
    return Buffer.from(trimmed, "hex");
  }
  if (/^[A-Za-z0-9+/]+={0,2}$/.test(trimmed) && trimmed.length % 4 === 0) {
    const decoded = Buffer.from(trimmed, "base64");
    if (decoded.toString("base64") === trimmed) {
      return decoded;
    }
  }
  throw new RuntimeDataBindingError(
    "report_data must be hex or standard base64",
  );
}

function digestAlgorithmFor(length: number): HashAlgorithm {
  if (length === 32) return "SHA-256";
  if (length === 48) return "SHA-384";
  throw new RuntimeDataBindingError(
    `cannot infer hash algorithm from report_data length ${length} (expected 32 or 48 bytes)`,
  );
}

export interface RuntimeDataBinding {
  readonly runtimeData: RuntimeData;
  readonly reportData: Buffer;
  readonly hashAlgorithm: HashAlgorithm;
}

/**
 * Verify that the token's runtime-data is the exact object whose hash sits in
 * the report's REPORT_DATA. This is what stops a compromised host from
 * swapping the wrapping key: any change to runtime-data changes the hash and
 * breaks the quote binding.
 */
export function verifyRuntimeDataBinding(
  claims: JsonObject,
  options: {
    runtimeDataPath?: string;
    reportDataPaths?: ReadonlyArray<string>;
    runtimeDataHash?: HashAlgorithm | "auto";
  } = {},
): RuntimeDataBinding {
  const runtimeDataPath = options.runtimeDataPath ?? "runtime-data";
  const reportDataPaths =
    options.reportDataPaths ??
    [
      "report_data",
      "report-data",
      "tdx.report_data",
      "tdx.report-data",
      "sgx.report_data",
      "sgx.report-data",
      "verifier_output.report_data",
    ];

  const runtimeClaim = resolveClaim(claims, runtimeDataPath);
  if (!runtimeClaim.found) {
    throw new RuntimeDataBindingError(
      `token has no runtime-data at claim "${runtimeDataPath}"`,
    );
  }
  const runtimeData = runtimeClaim.value;
  if (
    runtimeData === null ||
    typeof runtimeData !== "object" ||
    Array.isArray(runtimeData)
  ) {
    throw new RuntimeDataBindingError(
      "runtime-data must be a JSON object carrying the wrapping public key",
    );
  }

  let encodedReportData: string | undefined;
  let matchedPath: string | undefined;
  for (const path of reportDataPaths) {
    const candidate = resolveClaim(claims, path);
    if (candidate.found && typeof candidate.value === "string") {
      encodedReportData = candidate.value;
      matchedPath = path;
      break;
    }
  }
  if (encodedReportData === undefined) {
    throw new RuntimeDataBindingError(
      "token has no string report_data claim to bind runtime-data against",
    );
  }

  const reportData = decodeReportData(encodedReportData);
  const hashAlgorithm =
    options.runtimeDataHash === undefined ||
    options.runtimeDataHash === "auto"
      ? digestAlgorithmFor(reportData.length)
      : options.runtimeDataHash;

  const digest = hashRuntimeData(runtimeData, hashAlgorithm);
  if (digest.length !== reportData.length) {
    throw new RuntimeDataBindingError(
      `runtime-data ${hashAlgorithm} digest length ${digest.length} != report_data length ${reportData.length} at "${matchedPath}"`,
    );
  }
  if (!timingSafeEqual(digest, reportData)) {
    throw new RuntimeDataBindingError(
      "runtime-data hash does not match report_data: the attested runtime data was tampered with",
    );
  }

  return { runtimeData, reportData, hashAlgorithm };
}

/**
 * Pull the wrapping key out of the attested runtime-data. The key therefore
 * comes only from data the hardware quote binds to — never from a
 * caller-supplied field the host could control.
 */
export function extractWrappingKey(
  runtimeData: RuntimeData,
  publicKeyPaths: ReadonlyArray<string> = [
    "public",
    "public-key",
    "pubkey",
    "wrapping-key",
  ],
): { publicKey: string; path: string } {
  for (const path of publicKeyPaths) {
    const candidate = resolveClaim(runtimeData, path);
    if (candidate.found && typeof candidate.value === "string" && candidate.value.length > 0) {
      return { publicKey: candidate.value, path };
    }
  }
  throw new RuntimeDataBindingError(
    `no wrapping public key found in runtime-data (tried ${publicKeyPaths.join(", ")})`,
  );
}

/** Test helper: build the report_data value an attester would produce. */
export function buildReportData(
  runtimeData: RuntimeData,
  algorithm: HashAlgorithm = "SHA-384",
): string {
  return hashRuntimeData(runtimeData, algorithm).toString("hex");
}
