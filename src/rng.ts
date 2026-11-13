export type RandomNumberGenerator = () => number;

/**
 * Deterministic, well-distributed 32-bit PRNG. The same seed always produces
 * the same stream, which makes repeated browsingTopics() calls for the same
 * (top-level site, caller, epoch) tuple return identical results.
 */
export function mulberry32(seed: number): RandomNumberGenerator {
  let state = seed >>> 0;
  return function nextRandom(): number {
    state = (state + 0x6d2b79f5) >>> 0;
    let value = state;
    value = Math.imul(value ^ (value >>> 15), value | 1);
    value ^= value + Math.imul(value ^ (value >>> 7), value | 61);
    return ((value ^ (value >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * Derives the PRNG seed for one (top-level site, caller, epoch) draw.
 *
 * The epoch seed is generated independently per weekly epoch, so two epochs
 * keyed by the same site/caller derive unrelated streams: a caller cannot
 * correlate the random outcomes of different epochs. Within one epoch the
 * derivation is fully deterministic given the host-held epoch seed.
 */
export async function deriveRngSeed(
  epochSeed: string,
  topLevelSite: string,
  callerOrigin: string,
): Promise<number> {
  const encoder = new TextEncoder();
  const key = await crypto.subtle.importKey(
    'raw',
    encoder.encode(epochSeed),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign'],
  );
  const signature = await crypto.subtle.sign(
    'HMAC',
    key,
    encoder.encode(`${topLevelSite}\u0000${callerOrigin}`),
  );
  return new DataView(signature).getUint32(0, true) >>> 0;
}

export async function createDeterministicRng(
  epochSeed: string,
  topLevelSite: string,
  callerOrigin: string,
): Promise<RandomNumberGenerator> {
  const seed = await deriveRngSeed(epochSeed, topLevelSite, callerOrigin);
  return mulberry32(seed);
}
