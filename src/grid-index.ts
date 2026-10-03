import type { IndexEntry, TrackPoint, Trajectory } from './types.ts';

export interface CandidateRange {
  start: number;
  end: number;
}

/**
 * 时间切片倒排网格索引：
 *   slice(时间段) -> cell(空间格子) -> [实体位置条目]
 * 每条轨迹段会登记到它覆盖的所有时间切片中（取切片中点的插值位置），
 * 因此任意时刻落在某格子内的实体都能被 O(1) 反查。
 */
export class SpacetimeGridIndex {
  readonly cellSizeMeters: number;
  readonly sliceMs: number;
  private slices = new Map<number, Map<string, IndexEntry[]>>();
  private trajectories = new Map<string, Trajectory>();

  constructor(cellSizeMeters = 10, sliceMs = 1000) {
    this.cellSizeMeters = cellSizeMeters;
    this.sliceMs = sliceMs;
  }

  addTrajectory(traj: Trajectory): void {
    const pts = traj.points;
    if (pts.length === 0) return;
    this.trajectories.set(traj.entityId, traj);
    if (pts.length === 1) {
      this.insert(traj.entityId, pts[0]);
      return;
    }
    for (let i = 0; i < pts.length - 1; i++) {
      const p0 = pts[i];
      const p1 = pts[i + 1];
      const s0 = Math.floor(p0.t / this.sliceMs);
      const s1 = Math.floor(p1.t / this.sliceMs);
      for (let s = s0; s <= s1; s++) {
        const tm = Math.min(Math.max((s + 0.5) * this.sliceMs, p0.t), p1.t);
        const ratio = p1.t === p0.t ? 0 : (tm - p0.t) / (p1.t - p0.t);
        this.insert(traj.entityId, {
          t: tm,
          x: p0.x + (p1.x - p0.x) * ratio,
          y: p0.y + (p1.y - p0.y) * ratio,
          floor: ratio < 0.5 ? p0.floor : p1.floor,
        });
      }
    }
  }

  getTrajectory(entityId: string): Trajectory | undefined {
    return this.trajectories.get(entityId);
  }

  private insert(entityId: string, p: TrackPoint): void {
    const sliceIdx = Math.floor(p.t / this.sliceMs);
    let slice = this.slices.get(sliceIdx);
    if (!slice) {
      slice = new Map();
      this.slices.set(sliceIdx, slice);
    }
    const key = this.cellKey(p.x, p.y, p.floor);
    const bucket = slice.get(key);
    const entry: IndexEntry = { entityId, t: p.t, x: p.x, y: p.y, floor: p.floor };
    if (bucket) bucket.push(entry);
    else slice.set(key, [entry]);
  }

  private cellKey(x: number, y: number, floor?: number): string {
    const cx = Math.floor(x / this.cellSizeMeters);
    const cy = Math.floor(y / this.cellSizeMeters);
    return `${cx},${cy},${floor ?? 'na'}`;
  }

  /**
   * 反向检索：两实体在哪些时间切片内落入相邻格子（距离可能 < radius）。
   * 返回合并后的候选时间范围（首尾各扩一个切片兜底）。
   */
  candidateRanges(entityA: string, entityB: string, radiusMeters: number): CandidateRange[] {
    const k = Math.ceil(radiusMeters / this.cellSizeMeters);
    const hitSlices: number[] = [];
    for (const [sliceIdx, slice] of this.slices) {
      let hit = false;
      for (const [key, entries] of slice) {
        if (hit) break;
        if (!entries.some((e) => e.entityId === entityA)) continue;
        const [cx, cy, cf] = key.split(',');
        for (let dx = -k; dx <= k && !hit; dx++) {
          for (let dy = -k; dy <= k && !hit; dy++) {
            const nKey = `${Number(cx) + dx},${Number(cy) + dy},${cf}`;
            const bucket = slice.get(nKey);
            if (bucket && bucket.some((e) => e.entityId === entityB)) {
              hit = true;
            }
          }
        }
      }
      if (hit) hitSlices.push(sliceIdx);
    }
    hitSlices.sort((a, b) => a - b);
    const ranges: CandidateRange[] = [];
    for (const s of hitSlices) {
      const start = s * this.sliceMs;
      const end = (s + 1) * this.sliceMs;
      const last = ranges[ranges.length - 1];
      if (last && start <= last.end) {
        last.end = Math.max(last.end, end);
      } else {
        ranges.push({ start, end });
      }
    }
    return ranges.map((r) => ({
      start: r.start - this.sliceMs,
      end: r.end + this.sliceMs,
    }));
  }

  get sliceCount(): number {
    return this.slices.size;
  }
}
