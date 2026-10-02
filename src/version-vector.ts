/**
 * 版本向量：记录每个副本已被观察到的连续操作序号高水位。
 */
export type VersionVector = Record<string, number>;

export function mergeVersionVectors(a: VersionVector, b: VersionVector): VersionVector {
  const out: VersionVector = { ...a };
  for (const [replica, seq] of Object.entries(b)) {
    out[replica] = Math.max(out[replica] ?? 0, seq);
  }
  return out;
}

/** 逐副本取最小值，用于计算“所有对等端都已确认”的回收水位。 */
export function minVersionVectors(a: VersionVector, b: VersionVector): VersionVector {
  const out: VersionVector = {};
  for (const replica of new Set([...Object.keys(a), ...Object.keys(b)])) {
    out[replica] = Math.min(a[replica] ?? 0, b[replica] ?? 0);
  }
  return out;
}
