import { SpacetimeGridIndex } from './grid-index.ts';
import { buildProof, issueCertificates, type IntersectionProof } from './certificate.ts';
import type { ContactInterval, TrackPoint, Trajectory, Zone } from './types.ts';

const EPS = 1e-9;
const MERGE_GAP_MS = 1; // 相邻区间合并容差（毫秒）

interface Vec3 { x: number; y: number; z: number }

function floorOf(p: TrackPoint): number | undefined {
  return p.floor;
}

/** 二分查找 + 线性插值，取实体在时刻 t 的位置（t 须在轨迹覆盖范围内）。 */
export function sampleAt(points: TrackPoint[], t: number, floorHeight: number): Vec3 {
  let lo = 0;
  let hi = points.length - 1;
  if (t <= points[lo].t) return toVec(points[lo], floorHeight);
  if (t >= points[hi].t) return toVec(points[hi], floorHeight);
  while (lo + 1 < hi) {
    const mid = (lo + hi) >> 1;
    if (points[mid].t <= t) lo = mid;
    else hi = mid;
  }
  const p0 = points[lo];
  const p1 = points[hi];
  const ratio = (t - p0.t) / (p1.t - p0.t);
  return {
    x: p0.x + (p1.x - p0.x) * ratio,
    y: p0.y + (p1.y - p0.y) * ratio,
    z: (ratio < 0.5 ? floorOf(p0) ?? 0 : floorOf(p1) ?? 0) * floorHeight,
  };
}

function toVec(p: TrackPoint, floorHeight: number): Vec3 {
  return { x: p.x, y: p.y, z: (p.floor ?? 0) * floorHeight };
}

/**
 * 精确求交：把两轨迹的重叠时段切成若干“双方均做匀速直线运动”的单元段，
 * 在每段上解二次不等式 |pA(t) - pB(t)|^2 <= r^2，再合并相邻区间。
 * 可限定在候选时间范围内（配合索引加速）。
 */
export function computeContactIntervals(
  a: Trajectory,
  b: Trajectory,
  radiusMeters: number,
  floorHeight = 4,
  ranges?: Array<{ start: number; end: number }>,
): ContactInterval[] {
  const pa = a.points;
  const pb = b.points;
  if (pa.length === 0 || pb.length === 0) return [];

  const overlapStart = Math.max(pa[0].t, pb[0].t);
  const overlapEnd = Math.min(pa[pa.length - 1].t, pb[pb.length - 1].t);
  if (overlapStart >= overlapEnd) return [];

  const windows = ranges ?? [{ start: overlapStart, end: overlapEnd }];
  const raw: ContactInterval[] = [];

  for (const win of windows) {
    const wStart = Math.max(win.start, overlapStart);
    const wEnd = Math.min(win.end, overlapEnd);
    if (wStart >= wEnd) continue;

    // 单元段边界 = 两轨迹采样时刻的并集（裁剪到窗口内）
    const bounds = new Set<number>([wStart, wEnd]);
    for (const p of pa) if (p.t > wStart && p.t < wEnd) bounds.add(p.t);
    for (const p of pb) if (p.t > wStart && p.t < wEnd) bounds.add(p.t);
    const ts = [...bounds].sort((x, y) => x - y);

    for (let i = 0; i < ts.length - 1; i++) {
      const t0 = ts[i];
      const t1 = ts[i + 1];
      const dt = t1 - t0;
      if (dt <= 0) continue;

      const a0 = sampleAt(pa, t0, floorHeight);
      const a1 = sampleAt(pa, t1, floorHeight);
      const b0 = sampleAt(pb, t0, floorHeight);
      const b1 = sampleAt(pb, t1, floorHeight);

      // 相对运动 d(s) = p + v*s, s ∈ [0, dt]
      const p: Vec3 = { x: a0.x - b0.x, y: a0.y - b0.y, z: a0.z - b0.z };
      const v: Vec3 = {
        x: (a1.x - a0.x - (b1.x - b0.x)) / dt,
        y: (a1.y - a0.y - (b1.y - b0.y)) / dt,
        z: (a1.z - a0.z - (b1.z - b0.z)) / dt,
      };

      // 解 A s^2 + B s + C <= 0，其中 |d(s)|^2 - r^2
      const A = v.x * v.x + v.y * v.y + v.z * v.z;
      const B = 2 * (p.x * v.x + p.y * v.y + p.z * v.z);
      const C = p.x * p.x + p.y * p.y + p.z * p.z - radiusMeters * radiusMeters;

      let segStart: number | null = null;
      let segEnd: number | null = null;

      if (A < EPS) {
        if (C <= 0) {
          segStart = 0;
          segEnd = dt;
        }
      } else {
        const disc = B * B - 4 * A * C;
        if (disc >= 0) {
          const sq = Math.sqrt(disc);
          const s1 = (-B - sq) / (2 * A);
          const s2 = (-B + sq) / (2 * A);
          const lo = Math.max(0, s1);
          const hi = Math.min(dt, s2);
          if (lo <= hi + EPS) {
            segStart = lo;
            segEnd = hi;
          }
        }
      }

      if (segStart === null || segEnd === null) continue;

      // 段内最近距离时刻：顶点 s* = -B/(2A)，裁剪到 [segStart, segEnd]
      let sMin = (segStart + segEnd) / 2;
      if (A >= EPS) {
        sMin = Math.min(Math.max(-B / (2 * A), segStart), segEnd);
      }
      const dm = {
        x: p.x + v.x * sMin,
        y: p.y + v.y * sMin,
        z: p.z + v.z * sMin,
      };
      raw.push({
        start: t0 + segStart,
        end: t0 + segEnd,
        minDistance: Math.sqrt(dm.x * dm.x + dm.y * dm.y + dm.z * dm.z),
        minDistanceAt: t0 + sMin,
      });
    }
  }

  // 合并相邻/重叠区间
  raw.sort((x, y) => x.start - y.start);
  const merged: ContactInterval[] = [];
  for (const iv of raw) {
    const last = merged[merged.length - 1];
    if (last && iv.start <= last.end + MERGE_GAP_MS) {
      last.end = Math.max(last.end, iv.end);
      if (iv.minDistance < last.minDistance) {
        last.minDistance = iv.minDistance;
        last.minDistanceAt = iv.minDistanceAt;
      }
    } else {
      merged.push({ ...iv });
    }
  }
  return merged;
}

function resolveZone(zones: Zone[] | undefined, x: number, y: number, floor: number | null): string | null {
  if (!zones || zones.length === 0) return null;
  let best: Zone | null = null;
  let bestDist = Infinity;
  for (const z of zones) {
    if (floor !== null && z.floor !== undefined && z.floor !== floor) continue;
    const d = Math.hypot(z.x - x, z.y - y);
    if (d < bestDist) {
      bestDist = d;
      best = z;
    }
  }
  if (best && bestDist <= best.radius) return best.name;
  return best ? `${best.name}附近(${bestDist.toFixed(1)}m)` : null;
}

export interface ProveOptions {
  floorHeight?: number;
  zones?: Zone[];
  generatedAt?: number;
}

/**
 * 端到端证明：索引快速筛出候选时间切片 -> 精确求交 -> 签发链式地点证书。
 */
export function proveIntersection(
  index: SpacetimeGridIndex,
  entityA: string,
  entityB: string,
  radiusMeters: number,
  options: ProveOptions = {},
): IntersectionProof {
  const floorHeight = options.floorHeight ?? 4;
  const a = index.getTrajectory(entityA);
  const b = index.getTrajectory(entityB);
  if (!a || !b) throw new Error('实体轨迹未入库');

  const candidates = index.candidateRanges(entityA, entityB, radiusMeters);
  const intervals = computeContactIntervals(a, b, radiusMeters, floorHeight, candidates);

  const locations = intervals.map((iv) => {
    const pa = sampleAt(a.points, iv.minDistanceAt, floorHeight);
    const pb = sampleAt(b.points, iv.minDistanceAt, floorHeight);
    const x = (pa.x + pb.x) / 2;
    const y = (pa.y + pb.y) / 2;
    const floor = pa.z === pb.z ? Math.round(pa.z / floorHeight) : null;
    return { x, y, floor, zone: resolveZone(options.zones, x, y, floor) };
  });

  const certs = issueCertificates(entityA, entityB, intervals, locations);
  return buildProof(radiusMeters, [a, b], certs, options.generatedAt ?? Date.now());
}
