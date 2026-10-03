import { connectedComponents } from './connectivity.ts';
import type { Point, Road, RoadBreakpoint } from './types.ts';

export interface RoadAnalysisContext {
  rows: number;
  cols: number;
  cellSize: number;
  /** 积水深度阈值（米），与仿真器 floodThreshold 对齐。 */
  floodThreshold: number;
}

/** 将道路折线按固定间距采样为栅格坐标点。 */
export function sampleRoad(road: Road, step: number): Point[] {
  const pts: Point[] = [];
  const v = road.vertices;
  if (v.length === 0) return pts;
  if (v.length === 1) return [v[0]];
  for (let s = 0; s < v.length - 1; s++) {
    const a = v[s];
    const b = v[s + 1];
    const dx = b.x - a.x;
    const dy = b.y - a.y;
    const len = Math.hypot(dx, dy);
    const n = Math.max(1, Math.ceil(len / step));
    for (let k = 0; k < n; k++) {
      pts.push({ x: a.x + (dx * k) / n, y: a.y + (dy * k) / n });
    }
  }
  pts.push(v[v.length - 1]);
  return pts;
}

/** 栅格坐标点 -> 单元索引；越界返回 -1。 */
function pointToCell(ctx: RoadAnalysisContext, p: Point): { row: number; col: number; index: number } {
  const col = Math.floor(p.x / ctx.cellSize);
  const row = Math.floor(p.y / ctx.cellSize);
  if (row < 0 || row >= ctx.rows || col < 0 || col >= ctx.cols) {
    return { row: -1, col: -1, index: -1 };
  }
  return { row, col, index: row * ctx.cols + col };
}

/**
 * 计算指定 tick 全部被水体阻断的道路断点。
 *
 * 对每条道路沿中心线采样，采样点落入积水深度超过阈值的单元即构成断点；
 * 同一连通淹没区域内、同一道路上的相邻阻断采样点合并为一个断点
 * （取该连续阻断段的中点作为断点位置）。
 *
 * @param roads 道路列表
 * @param depth 该 tick 的积水深度栅格
 * @param tick 当前 tick 序号
 * @param ctx 栅格上下文
 */
export function findRoadBreakpoints(
  roads: readonly Road[],
  depth: Readonly<Float64Array>,
  tick: number,
  ctx: RoadAnalysisContext,
): RoadBreakpoint[] {
  // 为断点标注所属连通区域
  const mask = new Uint8Array(depth.length);
  for (let i = 0; i < depth.length; i++) {
    if (depth[i] > ctx.floodThreshold) mask[i] = 1;
  }
  const comps = connectedComponents({ rows: ctx.rows, cols: ctx.cols, data: mask });
  const cellRegion = new Int32Array(depth.length).fill(-1);
  comps.forEach((comp, id) => {
    for (const i of comp) cellRegion[i] = id;
  });

  const breakpoints: RoadBreakpoint[] = [];
  const step = ctx.cellSize / 2;
  for (const road of roads) {
    const samples = sampleRoad(road, step);
    // 连续阻断段：[startIdx, endIdx]（采样点下标）
    let segStart = -1;
    const flush = (segEnd: number) => {
      if (segStart < 0) return;
      const mid = samples[Math.floor((segStart + segEnd) / 2)];
      const cell = pointToCell(ctx, mid);
      if (cell.index >= 0) {
        breakpoints.push({
          roadId: road.id,
          tick,
          position: mid,
          cell: { row: cell.row, col: cell.col },
          waterDepth: depth[cell.index],
          regionId: cellRegion[cell.index] >= 0 ? cellRegion[cell.index] : null,
        });
      }
      segStart = -1;
    };
    for (let k = 0; k < samples.length; k++) {
      const cell = pointToCell(ctx, samples[k]);
      const blocked = cell.index >= 0 && depth[cell.index] > ctx.floodThreshold;
      if (blocked) {
        if (segStart < 0) segStart = k;
      } else {
        flush(k - 1);
      }
    }
    flush(samples.length - 1);
  }
  return breakpoints;
}
