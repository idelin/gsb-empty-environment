import { NEIGHBORS4 } from './dem.ts';
import type { FloodedRegion, Point } from './types.ts';

interface Mask {
  rows: number;
  cols: number;
  data: Uint8Array;
}

/**
 * 提取淹没掩码中的 4 连通区域。
 * 返回每个区域的单元索引列表。
 */
export function connectedComponents(mask: Mask): number[][] {
  const { rows, cols, data } = mask;
  const visited = new Uint8Array(data.length);
  const components: number[][] = [];
  const stack: number[] = [];
  for (let start = 0; start < data.length; start++) {
    if (!data[start] || visited[start]) continue;
    const comp: number[] = [];
    stack.push(start);
    visited[start] = 1;
    while (stack.length > 0) {
      const i = stack.pop() as number;
      comp.push(i);
      const r = Math.floor(i / cols);
      const c = i % cols;
      for (const [dr, dc] of NEIGHBORS4) {
        const nr = r + dr;
        const nc = c + dc;
        if (nr < 0 || nr >= rows || nc < 0 || nc >= cols) continue;
        const j = nr * cols + nc;
        if (data[j] && !visited[j]) {
          visited[j] = 1;
          stack.push(j);
        }
      }
    }
    components.push(comp);
  }
  return components;
}

type VertexKey = string;

function vkey(x: number, y: number): VertexKey {
  return `${x},${y}`;
}

/**
 * 对一组连通淹没单元做边界多边形追踪。
 *
 * 原理：对每个淹没单元的四条边，若邻侧不是淹没单元，则该边属于边界；
 * 以固定绕行方向（y 向下坐标系中顺时针）生成有向边，边界边恰好首尾相接
 * 成若干闭合环；按有向面积绝对值最大者判定为外环，其余为孔洞。
 *
 * @param cells 区域内单元索引（r*cols+c）
 * @param cols 栅格列数
 * @param cellSize 单元边长（米）
 * @returns { outer, holes } 顶点坐标（栅格坐标系，米）
 */
export function traceBoundary(
  cells: readonly number[],
  cols: number,
  cellSize: number,
): { outer: Point[]; holes: Point[][] } {
  const cellSet = new Set(cells);
  // 有向边：key = 起点，value = 终点（格点单位坐标）
  const edges = new Map<VertexKey, [number, number]>();

  const addEdge = (x1: number, y1: number, x2: number, y2: number) => {
    edges.set(vkey(x1, y1), [x2, y2]);
  };

  for (const i of cells) {
    const r = Math.floor(i / cols);
    const c = i % cols;
    // 上邻非淹没 -> 顶边（向右）
    if (r === 0 || !cellSet.has((r - 1) * cols + c)) {
      addEdge(c, r, c + 1, r);
    }
    // 右邻非淹没 -> 右边（向下）
    if (c === cols - 1 || !cellSet.has(r * cols + c + 1)) {
      addEdge(c + 1, r, c + 1, r + 1);
    }
    // 下邻非淹没 -> 底边（向左）
    if (!cellSet.has((r + 1) * cols + c)) {
      addEdge(c + 1, r + 1, c, r + 1);
    }
    // 左邻非淹没 -> 左边（向上）
    if (c === 0 || !cellSet.has(r * cols + c - 1)) {
      addEdge(c, r + 1, c, r);
    }
  }

  // 沿边链闭合为环
  const rings: Point[][] = [];
  while (edges.size > 0) {
    const [startKey, [nx, ny]] = edges.entries().next().value as [VertexKey, [number, number]];
    const [sx, sy] = startKey.split(',').map(Number);
    const ring: Point[] = [{ x: sx * cellSize, y: sy * cellSize }];
    edges.delete(startKey);
    let cx = nx;
    let cy = ny;
    while (!(cx === sx && cy === sy)) {
      ring.push({ x: cx * cellSize, y: cy * cellSize });
      const next = edges.get(vkey(cx, cy));
      if (!next) throw new Error('边界边链断裂：掩码拓扑异常');
      edges.delete(vkey(cx, cy));
      [cx, cy] = next;
    }
    rings.push(ring);
  }

  if (rings.length === 0) return { outer: [], holes: [] };
  // 去除共线中间点，得到紧凑多边形
  const simplified = rings.map(simplifyRing);
  // 有向面积绝对值最大者为外环
  let outerIdx = 0;
  let maxArea = -1;
  for (let i = 0; i < simplified.length; i++) {
    const a = Math.abs(signedArea(simplified[i]));
    if (a > maxArea) {
      maxArea = a;
      outerIdx = i;
    }
  }
  const outer = simplified[outerIdx];
  const holes = simplified.filter((_, i) => i !== outerIdx);
  return { outer, holes };
}

/** 移除闭环上的共线点（方向未改变的中间格点）。 */
function simplifyRing(ring: Point[]): Point[] {
  if (ring.length <= 3) return ring;
  const out: Point[] = [];
  const n = ring.length;
  for (let i = 0; i < n; i++) {
    const prev = ring[(i - 1 + n) % n];
    const cur = ring[i];
    const next = ring[(i + 1) % n];
    const d1x = cur.x - prev.x;
    const d1y = cur.y - prev.y;
    const d2x = next.x - cur.x;
    const d2y = next.y - cur.y;
    // 叉积为 0 且同向 -> 共线，丢弃
    if (d1x * d2y - d1y * d2x === 0 && d1x * d2x + d1y * d2y > 0) continue;
    out.push(cur);
  }
  return out;
}

/** 鞋带公式有向面积。 */
export function signedArea(ring: readonly Point[]): number {
  let sum = 0;
  for (let i = 0; i < ring.length; i++) {
    const p = ring[i];
    const q = ring[(i + 1) % ring.length];
    sum += p.x * q.y - q.x * p.y;
  }
  return sum / 2;
}

/**
 * 计算指定 tick 的全部淹没连通区域及边界多边形。
 *
 * @param mask 淹没掩码
 * @param depth 对应 tick 的积水深度栅格
 * @param cellSize 单元边长（米）
 */
export function floodedRegions(
  mask: Mask,
  depth: Readonly<Float64Array>,
  cellSize: number,
): FloodedRegion[] {
  const comps = connectedComponents(mask);
  const regions: FloodedRegion[] = [];
  for (let id = 0; id < comps.length; id++) {
    const comp = comps[id];
    let maxDepth = 0;
    for (const i of comp) {
      if (depth[i] > maxDepth) maxDepth = depth[i];
    }
    const { outer, holes } = traceBoundary(comp, mask.cols, cellSize);
    regions.push({
      id,
      cellCount: comp.length,
      areaSqm: comp.length * cellSize * cellSize,
      maxDepth,
      outerRing: outer,
      holes,
    });
  }
  return regions;
}
