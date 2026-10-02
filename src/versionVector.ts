/**
 * Version vector: replicaId -> highest contiguous sequence number observed
 * from that replica. Used both as CRDT version summary and as the
 * acknowledgment watermark exchanged by the sync protocol.
 */
export type VersionVector = Record<string, number>;

export function getSeq(vv: VersionVector, replica: string): number {
  return vv[replica] ?? 0;
}

export function cloneVV(vv: VersionVector): VersionVector {
  return { ...vv };
}

/** Component-wise maximum (join). Returns a new object. */
export function mergeVV(a: VersionVector, b: VersionVector): VersionVector {
  const out: VersionVector = { ...a };
  for (const [replica, seq] of Object.entries(b)) {
    if (seq > (out[replica] ?? 0)) out[replica] = seq;
  }
  return out;
}

/** True if `a` covers every component of `b` (a >= b). */
export function coversVV(a: VersionVector, b: VersionVector): boolean {
  for (const [replica, seq] of Object.entries(b)) {
    if ((a[replica] ?? 0) < seq) return false;
  }
  return true;
}

export function equalVV(a: VersionVector, b: VersionVector): boolean {
  return coversVV(a, b) && coversVV(b, a);
}
