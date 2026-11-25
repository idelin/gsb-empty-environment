import { SimulatedCryptoBackend } from "./crypto.ts";
import { Verifier } from "./verifier.ts";

export * from "./types.ts";
export { Verifier, enumerateSubsets } from "./verifier.ts";
export {
  contains,
  coversAll,
  isValidRange,
  overlaps,
  validateCanonicalMap,
  identityMap,
} from "./ranges.ts";
export { buildCanonicalSpec } from "./canonicalize.ts";
export type { BuildOptions } from "./canonicalize.ts";
export { SimulatedCryptoBackend, signToken, tamperedToken } from "./crypto.ts";

/** Ready-to-use verifier wired to the deterministic simulated backend. */
export function createDefaultVerifier(): Verifier {
  return new Verifier(new SimulatedCryptoBackend());
}
