import type { TrajectoryPoint, Vec3 } from "./types.ts";

export interface GridIndexOptions {
  /** 时间切片长度（毫秒），默认 5 分钟。 */
  sliceDurationMs?: number;
  /** 空间网格边长（米），默认 20 米。 */
  cellSizeMeters?: number;
}

interface SliceBucket {
  /** 倒排网格：数值化 cellKey -> 该切片内落入此格的实体集合。 */
  cells: Map<number, Set<string>>;
  /** 每个实体在本切片内的网格集合（邻域候选预筛用）。 */
  cellsByEntity: Map<string, Set<number>>;
  /** 每个实体在本切片内的网格包围盒（O(1) 粗筛）。 */
  boundsByEntity: Map<string, CellBounds>;
  /** entityId -> (时间戳 -> 轨迹点)，按时间戳去重。 */
  points: Map<string, Map<number, TrajectoryPoint>>;
}

interface CellBounds {
  minCx: number;
  maxCx: number;
  minCy: number;
  maxCy: number;
  minCz: number;
  maxCz: number;
}

/**
 * 网格坐标数值编码：每轴偏移 2^15、步长 2^16，键为线性组合，
 * 邻格键可通过整数加减直接得到（key + dx*S2 + dy*S + dz）。
 * 默认 20m 格宽下覆盖 ±655km 范围。
 */
const CELL_STRIDE = 65536;
const CELL_STRIDE2 = CELL_STRIDE * CELL_STRIDE;
const CELL_OFFSET = 32768;

function encodeCell(cx: number, cy: number, cz: number): number {
  return (
    (cx + CELL_OFFSET) * CELL_STRIDE2 +
    (cy + CELL_OFFSET) * CELL_STRIDE +
    (cz + CELL_OFFSET)
  );
}

function lerpPoint(a: TrajectoryPoint, b: TrajectoryPoint, t: number): TrajectoryPoint {
  if (b.t === a.t) return { ...a, t };
  const u = (t - a.t) / (b.t - a.t);
  return {
    t,
    x: a.x + (b.x - a.x) * u,
    y: a.y + (b.y - a.y) * u,
    z: a.z + (b.z - a.z) * u,
  };
}

/**
 * 四维时空反向索引：时间被切成等长切片，每个切片内维护
 * “空间网格 -> 实体” 的倒排表，以及 “实体 -> 轨迹点” 的正排表。
 * 跨切片的轨迹段会在切片边界处插值出边界点，保证每个切片自包含。
 */
export class SpacetimeGridIndex {
  readonly sliceDurationMs: number;
  readonly cellSizeMeters: number;
  private readonly slices = new Map<number, SliceBucket>();
  private readonly entities = new Map<string, TrajectoryPoint[]>();

  constructor(options: GridIndexOptions = {}) {
    this.sliceDurationMs = options.sliceDurationMs ?? 300_000;
    this.cellSizeMeters = options.cellSizeMeters ?? 20;
  }

  sliceOf(t: number): number {
    return Math.floor(t / this.sliceDurationMs);
  }

  private cellCoordsOf(x: number, y: number, z: number): [number, number, number] {
    const c = this.cellSizeMeters;
    return [Math.floor(x / c), Math.floor(y / c), Math.floor(z / c)];
  }

  private bucket(slice: number): SliceBucket {
    let b = this.slices.get(slice);
    if (!b) {
      b = {
        cells: new Map(),
        cellsByEntity: new Map(),
        boundsByEntity: new Map(),
        points: new Map(),
      };
      this.slices.set(slice, b);
    }
    return b;
  }

  private addPoint(slice: number, entityId: string, p: TrajectoryPoint): void {
    const b = this.bucket(slice);
    let perEntity = b.points.get(entityId);
    if (!perEntity) {
      perEntity = new Map();
      b.points.set(entityId, perEntity);
    }
    perEntity.set(p.t, p);
    const [cx, cy, cz] = this.cellCoordsOf(p.x, p.y, p.z);
    this.registerCell(b, entityId, cx, cy, cz);
  }

  private registerCell(
    b: SliceBucket,
    entityId: string,
    cx: number,
    cy: number,
    cz: number,
  ): void {
    const key = encodeCell(cx, cy, cz);
    let cellSet = b.cells.get(key);
    if (!cellSet) {
      cellSet = new Set();
      b.cells.set(key, cellSet);
    }
    cellSet.add(entityId);
    let entityCells = b.cellsByEntity.get(entityId);
    if (!entityCells) {
      entityCells = new Set();
      b.cellsByEntity.set(entityId, entityCells);
    }
    entityCells.add(key);
    let bounds = b.boundsByEntity.get(entityId);
    if (!bounds) {
      bounds = {
        minCx: cx,
        maxCx: cx,
        minCy: cy,
        maxCy: cy,
        minCz: cz,
        maxCz: cz,
      };
      b.boundsByEntity.set(entityId, bounds);
    } else {
      bounds.minCx = Math.min(bounds.minCx, cx);
      bounds.maxCx = Math.max(bounds.maxCx, cx);
      bounds.minCy = Math.min(bounds.minCy, cy);
      bounds.maxCy = Math.max(bounds.maxCy, cy);
      bounds.minCz = Math.min(bounds.minCz, cz);
      bounds.maxCz = Math.max(bounds.maxCz, cz);
    }
  }

  /**
   * 用 3D DDA（Amanatides-Woo）遍历轨迹段扫过的全部网格并登记实体，
   * 保证快速移动的实体不会因只登记端点网格而漏检（预筛无假阴性）。
   */
  private addSegmentCells(
    b: SliceBucket,
    entityId: string,
    p: Vec3,
    q: Vec3,
  ): void {
    const c = this.cellSizeMeters;
    let cx = Math.floor(p.x / c);
    let cy = Math.floor(p.y / c);
    let cz = Math.floor(p.z / c);
    const ex = Math.floor(q.x / c);
    const ey = Math.floor(q.y / c);
    const ez = Math.floor(q.z / c);
    this.registerCell(b, entityId, cx, cy, cz);

    const dx = q.x - p.x;
    const dy = q.y - p.y;
    const dz = q.z - p.z;
    const stepX = Math.sign(dx);
    const stepY = Math.sign(dy);
    const stepZ = Math.sign(dz);
    const tDeltaX = dx !== 0 ? c / Math.abs(dx) : Infinity;
    const tDeltaY = dy !== 0 ? c / Math.abs(dy) : Infinity;
    const tDeltaZ = dz !== 0 ? c / Math.abs(dz) : Infinity;
    const boundary = (v: number, cell: number, step: number): number =>
      (cell + (step > 0 ? 1 : 0)) * c - v;
    let tMaxX = dx !== 0 ? boundary(p.x, cx, stepX) / dx : Infinity;
    let tMaxY = dy !== 0 ? boundary(p.y, cy, stepY) / dy : Infinity;
    let tMaxZ = dz !== 0 ? boundary(p.z, cz, stepZ) / dz : Infinity;

    const maxSteps =
      Math.abs(ex - cx) + Math.abs(ey - cy) + Math.abs(ez - cz);
    for (let i = 0; i < maxSteps; i++) {
      if (tMaxX <= tMaxY && tMaxX <= tMaxZ) {
        cx += stepX;
        tMaxX += tDeltaX;
      } else if (tMaxY <= tMaxZ) {
        cy += stepY;
        tMaxY += tDeltaY;
      } else {
        cz += stepZ;
        tMaxZ += tDeltaZ;
      }
      this.registerCell(b, entityId, cx, cy, cz);
    }
  }

  /** 插入一个实体的一段轨迹（点按时间排序后逐段写入切片）。 */
  insert(entityId: string, points: TrajectoryPoint[]): void {
    const pts = [...points].sort((p, q) => p.t - q.t);
    const existing = this.entities.get(entityId) ?? [];
    const merged = [...existing, ...pts].sort((p, q) => p.t - q.t);
    this.entities.set(entityId, merged);
    if (pts.length === 0) return;
    if (pts.length === 1) {
      this.addPoint(this.sliceOf(pts[0].t), entityId, pts[0]);
      return;
    }
    for (let i = 0; i + 1 < pts.length; i++) {
      const p = pts[i];
      const q = pts[i + 1];
      const s0 = this.sliceOf(p.t);
      const s1 = this.sliceOf(q.t);
      for (let s = s0; s <= s1; s++) {
        const lo = Math.max(p.t, s * this.sliceDurationMs);
        const hi = Math.min(q.t, (s + 1) * this.sliceDurationMs);
        const pLo = lerpPoint(p, q, lo);
        const pHi = lerpPoint(p, q, hi);
        this.addPoint(s, entityId, pLo);
        if (hi > lo) this.addPoint(s, entityId, pHi);
        this.addSegmentCells(this.bucket(s), entityId, pLo, pHi);
      }
    }
  }

  has(entityId: string): boolean {
    return this.entities.has(entityId);
  }

  entityIds(): string[] {
    return [...this.entities.keys()];
  }

  sliceCount(): number {
    return this.slices.size;
  }

  /** 实体的原始轨迹（未插值），按时间排序。 */
  trajectoryOf(entityId: string): TrajectoryPoint[] {
    return this.entities.get(entityId) ?? [];
  }

  /**
   * 反向索引预筛：返回两实体在时间切片上共存、且空间网格
   * 在阈值邻域内相交的全部切片编号。
   */
  candidateSlices(entityA: string, entityB: string, thresholdMeters: number): number[] {
    const k = Math.ceil(thresholdMeters / this.cellSizeMeters);
    const result: number[] = [];
    for (const [sliceNo, bucket] of this.slices) {
      const cellsA = bucket.cellsByEntity.get(entityA);
      const cellsB = bucket.cellsByEntity.get(entityB);
      if (!cellsA || !cellsB) continue;
      const boundsA = bucket.boundsByEntity.get(entityA);
      const boundsB = bucket.boundsByEntity.get(entityB);
      if (boundsA && boundsB) {
        if (
          boundsA.minCx - k > boundsB.maxCx ||
          boundsB.minCx - k > boundsA.maxCx ||
          boundsA.minCy - k > boundsB.maxCy ||
          boundsB.minCy - k > boundsA.maxCy ||
          boundsA.minCz - k > boundsB.maxCz ||
          boundsB.minCz - k > boundsA.maxCz
        ) {
          continue;
        }
      }
      let hit = false;
      for (const key of cellsA) {
        for (let dx = -k; dx <= k && !hit; dx++) {
          const baseX = key + dx * CELL_STRIDE2;
          for (let dy = -k; dy <= k && !hit; dy++) {
            const baseY = baseX + dy * CELL_STRIDE;
            for (let dz = -k; dz <= k && !hit; dz++) {
              if (cellsB.has(baseY + dz)) hit = true;
            }
          }
        }
        if (hit) break;
      }
      if (hit) result.push(sliceNo);
    }
    return result.sort((a, b) => a - b);
  }

  /** 取出实体在指定切片内的全部轨迹点（含切片边界插值点），按时间排序去重。 */
  pointsInSlices(entityId: string, sliceNumbers: Iterable<number>): TrajectoryPoint[] {
    const out: TrajectoryPoint[] = [];
    const sorted = [...sliceNumbers].sort((a, b) => a - b);
    for (const s of sorted) {
      const bucket = this.slices.get(s);
      const perEntity = bucket?.points.get(entityId);
      if (!perEntity) continue;
      const pts = [...perEntity.values()].sort((p, q) => p.t - q.t);
      for (const p of pts) {
        if (out.length > 0 && out[out.length - 1].t === p.t) continue;
        out.push(p);
      }
    }
    return out;
  }
}
