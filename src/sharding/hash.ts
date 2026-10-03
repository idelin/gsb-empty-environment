/** FNV-1a 32-bit: small, deterministic, dependency-free hash. */
export function fnv1a32(input: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < input.length; i++) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return hash >>> 0;
}

/** MurmurHash3 fmix32 finalizer: avalanches away FNV-1a's low-bit bias. */
export function fmix32(hash: number): number {
  let h = hash >>> 0;
  h ^= h >>> 16;
  h = Math.imul(h, 0x85ebca6b);
  h ^= h >>> 13;
  h = Math.imul(h, 0xc2b2ae35);
  h ^= h >>> 16;
  return h >>> 0;
}

export function vshardOfKey(key: string, vshardCount: number): number {
  return fmix32(fnv1a32(key)) % vshardCount;
}

/** Rendezvous (highest-random-weight) score of a node for a virtual shard. */
export function rendezvousScore(vshard: number, nodeId: string): number {
  return fmix32(fnv1a32(`${vshard}#${nodeId}`));
}
