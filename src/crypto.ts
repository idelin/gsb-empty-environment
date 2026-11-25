import type { CryptoBackend, DistributionBundle, SignatureRecord } from "./types.ts";

/**
 * Deterministic simulator for the acceptance package. Real hosts supply a
 * backend backed by their key store; this one treats a record as valid when
 * its `signatureBytes` carry the magic token produced by {@link signToken}.
 * The token includes index, algorithm and key material so a blob cannot be
 * copied across records or across algorithms.
 */
const MAGIC = 0xa5;

export function signToken(record: Pick<SignatureRecord, "index" | "algorithm" | "keyMaterialId">): Uint8Array {
  const text = `${MAGIC}|${record.index}|${record.algorithm}|${record.keyMaterialId}`;
  return new TextEncoder().encode(text);
}

export function tamperedToken(): Uint8Array {
  return new TextEncoder().encode("tampered");
}

export class SimulatedCryptoBackend implements CryptoBackend {
  readonly knownAlgorithms: ReadonlySet<string>;

  constructor(knownAlgorithms: readonly string[] = ["ed25519-v1", "ecdsa-p256-v2"]) {
    this.knownAlgorithms = new Set(knownAlgorithms);
  }

  verify(record: SignatureRecord, _bundle: DistributionBundle): boolean {
    const expected = signToken(record);
    if (expected.length !== record.signatureBytes.length) return false;
    return expected.every((byte, offset) => byte === record.signatureBytes[offset]);
  }
}
