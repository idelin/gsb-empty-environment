/**
 * Hand-rolled FNV-1a 32-bit hash — deterministic across platforms,
 * zero dependencies, good enough distribution for shard placement.
 */
export function fnv1a32(input: string | Uint8Array): number {
  const bytes = typeof input === 'string' ? new TextEncoder().encode(input) : input;
  let hash = 0x811c9dc5;
  for (let i = 0; i < bytes.length; i++) {
    hash ^= bytes[i];
    // 32-bit FNV prime multiply via shifts to stay in int32 precision.
    hash = (hash + ((hash << 1) + (hash << 4) + (hash << 7) + (hash << 8) + (hash << 24))) >>> 0;
  }
  return hash >>> 0;
}

/** 64-bit-style mixing hash for shard placement (two FNV passes, xored). */
export function shardHash(shardId: number, nodeId: string): number {
  return fnv1a32(`${shardId}:${nodeId}`);
}
