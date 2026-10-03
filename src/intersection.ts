import type { TrajectoryPoint, Vec3 } from "./types.ts";

export interface RawContactInterval {
  tStart: number;
  tEnd: number;
  minDistance: number;
  tClosest: number;
}

const MERGE_EPS_MS = 1e-6;

function sub(a: Vec3, b: Vec3): Vec3 {
  return { x: a.x - b.x, y: a.y - b.y, z: a.z - b.z };
}

function dot(a: Vec3, b: Vec3): number {
  return a.x * b.x + a.y * b.y + a.z * b.z;
}

/**
 * 计算两条分段线性轨迹在给定重叠时段内、距离严格小于 thresholdMeters 的
 * 全部时间区间。在每个“基本窗口”（两轨迹均为线性）内，相对位置的平方
 * 距离是关于归一化时间 u∈[0,1] 的二次函数，因此区间端点由求根公式精确给出。
 */
export function computeContactIntervals(
  a: TrajectoryPoint[],
  b: TrajectoryPoint[],
  thresholdMeters: number,
): RawContactInterval[] {
  if (a.length === 0 || b.length === 0) return [];
  const tMin = Math.max(a[0].t, b[0].t);
  const tMax = Math.min(a[a.length - 1].t, b[b.length - 1].t);
  if (!(tMin < tMax)) return [];

  // 合并双方折点时刻，得到基本窗口序列。
  const timeSet = new Set<number>([tMin, tMax]);
  for (const p of a) if (p.t > tMin && p.t < tMax) timeSet.add(p.t);
  for (const p of b) if (p.t > tMin && p.t < tMax) timeSet.add(p.t);
  const times = [...timeSet].sort((p, q) => p - q);

  const posA = samplePositions(a, times);
  const posB = samplePositions(b, times);
  const r2 = thresholdMeters * thresholdMeters;

  const merged: RawContactInterval[] = [];
  for (let w = 0; w + 1 < times.length; w++) {
    const t0 = times[w];
    const t1 = times[w + 1];
    const span = t1 - t0;
    if (span <= 0) continue;

    const r0 = sub(posA[w], posB[w]);
    const r1 = sub(posA[w + 1], posB[w + 1]);
    const v = sub(r1, r0); // 相对位移（u 从 0 到 1）
    const qa = dot(v, v);
    const qb = 2 * dot(r0, v);
    const qc = dot(r0, r0) - r2;

    // 窗口内最近点（用于 minDistance / tClosest）。
    let uMin = 0;
    if (qa > 1e-18) uMin = Math.min(1, Math.max(0, -qb / (2 * qa)));
    const dMin2 = qa * uMin * uMin + qb * uMin + (qc + r2);
    const dMin = Math.sqrt(Math.max(0, dMin2));
    const tClosest = t0 + uMin * span;

    // 求 d^2(u) < r^2 的子区间。
    let lo: number | null = null;
    let hi: number | null = null;
    if (qa <= 1e-18) {
      if (qc < 0) {
        lo = 0;
        hi = 1;
      }
    } else {
      const disc = qb * qb - 4 * qa * qc;
      if (disc > 0) {
        const sq = Math.sqrt(disc);
        const u1 = (-qb - sq) / (2 * qa);
        const u2 = (-qb + sq) / (2 * qa);
        const clo = Math.max(u1, 0);
        const chi = Math.min(u2, 1);
        if (clo < chi) {
          lo = clo;
          hi = chi;
        }
      } else if (qc < 0) {
        lo = 0;
        hi = 1;
      }
    }
    if (lo === null || hi === null) continue;

    const segStart = t0 + lo * span;
    const segEnd = t0 + hi * span;
    const last = merged[merged.length - 1];
    if (last && segStart <= last.tEnd + MERGE_EPS_MS) {
      last.tEnd = Math.max(last.tEnd, segEnd);
      if (dMin < last.minDistance) {
        last.minDistance = dMin;
        last.tClosest = tClosest;
      }
    } else {
      merged.push({ tStart: segStart, tEnd: segEnd, minDistance: dMin, tClosest });
    }
  }
  return merged;
}

/** 对每条轨迹在一组递增时刻上采样位置（双指针线性插值）。 */
function samplePositions(points: TrajectoryPoint[], times: number[]): Vec3[] {
  const out: Vec3[] = new Array(times.length);
  let i = 0;
  for (let k = 0; k < times.length; k++) {
    const t = times[k];
    while (i + 1 < points.length && points[i + 1].t < t) i++;
    const p = points[i];
    const q = points[Math.min(i + 1, points.length - 1)];
    if (q.t <= p.t || t <= p.t) {
      out[k] = { x: p.x, y: p.y, z: p.z };
    } else if (t >= q.t) {
      out[k] = { x: q.x, y: q.y, z: q.z };
    } else {
      const u = (t - p.t) / (q.t - p.t);
      out[k] = {
        x: p.x + (q.x - p.x) * u,
        y: p.y + (q.y - p.y) * u,
        z: p.z + (q.z - p.z) * u,
      };
    }
  }
  return out;
}

/** 在轨迹上插值取某一时刻的位置（t 越界时取端点）。 */
export function positionAt(points: TrajectoryPoint[], t: number): Vec3 {
  if (points.length === 0) throw new Error("empty trajectory");
  if (t <= points[0].t) return { x: points[0].x, y: points[0].y, z: points[0].z };
  const last = points[points.length - 1];
  if (t >= last.t) return { x: last.x, y: last.y, z: last.z };
  let lo = 0;
  let hi = points.length - 1;
  while (lo + 1 < hi) {
    const mid = (lo + hi) >> 1;
    if (points[mid].t <= t) lo = mid;
    else hi = mid;
  }
  const p = points[lo];
  const q = points[hi];
  const u = (t - p.t) / (q.t - p.t);
  return {
    x: p.x + (q.x - p.x) * u,
    y: p.y + (q.y - p.y) * u,
    z: p.z + (q.z - p.z) * u,
  };
}
