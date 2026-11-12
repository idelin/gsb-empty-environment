import type { IntegrityMetadata, IntegrityState } from "./types.ts";

/**
 * Subresource Integrity (SRI) metadata handling.
 *
 * The security rule enforced here is "missing metadata is never trusted":
 * an entry without validated integrity metadata is normalized to the
 * `unchecked` state, never upgraded to `verified`.
 */

const HASH_ALGORITHMS = new Set(["sha256", "sha384", "sha512"]);

const BASE64_RE = /^[A-Za-z0-9+/]+={0,2}$/;

export function isValidIntegrityMetadata(meta: IntegrityMetadata): boolean {
  if (!HASH_ALGORITHMS.has(meta.algorithm)) {
    return false;
  }
  if (!meta.digest || !BASE64_RE.test(meta.digest)) {
    return false;
  }
  return true;
}

/**
 * Parse the declared integrity block of a stored response.
 *
 * - `verified` requires at least one well-formed SRI metadata record;
 *   malformed records are rejected instead of silently trusted.
 * - An empty/missing metadata block collapses to `unchecked`, regardless of
 *   what state the caller claimed. This prevents a caller from marking an
 *   entry `verified` without carrying verifiable metadata.
 */
export function normalizeIntegrity(input: {
  state?: IntegrityState;
  metadata?: IntegrityMetadata[];
}): { state: IntegrityState; metadata: IntegrityMetadata[] } {
  const metadata = Array.isArray(input.metadata) ? input.metadata : [];

  if (metadata.length === 0) {
    if (input.state === "mismatch") {
      return { state: "mismatch", metadata: [] };
    }
    return { state: "unchecked", metadata: [] };
  }

 for (const record of metadata) {
    if (!isValidIntegrityMetadata(record)) {
      throw new TypeError(
        "Integrity metadata is malformed: algorithm must be sha256/sha384/sha512 " +
          "and digest must be base64",
      );
    }
  }

  if (input.state === "mismatch") {
    return { state: "mismatch", metadata };
  }
  // With well-formed metadata present, a declared verified state is honored;
  // absent/unknown state defaults to unchecked rather than trusted.
  return { state: input.state === "verified" ? "verified" : "unchecked", metadata };
}

/**
 * Integrity is only reusable when it was actually verified. `unchecked`
 * entries cannot satisfy a request that demands integrity, and `mismatch`
 * entries never satisfy anything.
 */
export function integritySatisfied(
  entryState: IntegrityState,
  requestRequiresIntegrity: boolean,
): boolean {
  if (entryState === "mismatch") {
    return false;
  }
  if (requestRequiresIntegrity && entryState !== "verified") {
    return false;
  }
  return true;
}
