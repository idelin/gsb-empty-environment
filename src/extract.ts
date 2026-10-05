import type { Contour, Mask, Point } from './types.js';

/** 单位有向边：起点格点 (sx, sy) + 方向 (dx, dy)，前景在行进方向左侧。 */
interface DirEdge {
  sx: number;
  sy: number;
  dx: number;
  dy: number;
}

function checkMask(mask: Mask): void {
  if (mask.width < 0 || mask.height < 0) {
    throw new Error('mask 尺寸非法');
  }
  if (mask.data.length !== mask.width * mask.height) {
    throw new Error('mask.data 长度必须等于 width * height');
  }
}

/**
 * 生成全部边界有向边。
 * 对每个前景像素，若其四邻居越界或为背景，则发出一条对应的有向边，
 * 方向保证前景在左侧（y 向下坐标系）。
 */
function buildEdges(mask: Mask): DirEdge[] {
  const w = mask.width;
  const h = mask.height;
  const data = mask.data;
  const edges: DirEdge[] = [];
  const at = (x: number, y: number): number =>
    x < 0 || y < 0 || x >= w || y >= h ? 0 : data[y * w + x];
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      if (at(x, y) === 0) continue;
      if (at(x, y - 1) === 0) edges.push({ sx: x + 1, sy: y, dx: -1, dy: 0 });
      if (at(x, y + 1) === 0) edges.push({ sx: x, sy: y + 1, dx: 1, dy: 0 });
      if (at(x - 1, y) === 0) edges.push({ sx: x, sy: y, dx: 0, dy: 1 });
      if (at(x + 1, y) === 0) edges.push({ sx: x + 1, sy: y + 1, dx: 0, dy: -1 });
    }
  }
  return edges;
}

/**
 * 后继边规则。
 *
 * 每个格点处入度等于出度：普通情形唯一出边；仅当两条前景像素在该顶点
 * 对角接触时出现两条出边。此时按 (dx, dy) -> (dy, -dx) 转向配对，
 * 使两条对角前景各自成环、背景在该顶点保持连通——这正是
 * “前景四邻域 / 背景八邻域”的 Jordan 一致性要求。
 * 规则是纯组合式的，不依赖任何浮点点内判断。
 */
function nextEdgeIndex(edges: DirEdge[], outOf: Map<number, number[]>, stride: number, index: number): number {
  const e = edges[index];
  const key = (e.sy + e.dy) * stride + (e.sx + e.dx);
  const candidates = outOf.get(key);
  if (candidates === undefined || candidates.length === 0) {
    throw new Error('边界边不平衡：缺少后继边');
  }
  if (candidates.length === 1) return candidates[0];
  const rx = e.dy;
  const ry = -e.dx;
  const a = edges[candidates[0]];
  return a.dx === rx && a.dy === ry ? candidates[0] : candidates[1];
}

function signedArea2(vertices: Point[]): number {
  let sum = 0;
  const n = vertices.length;
  for (let i = 0; i < n; i++) {
    const a = vertices[i];
    const b = vertices[(i + 1) % n];
    sum += a.x * b.y - b.x * a.y;
  }
  return sum;
}

/**
 * 从二值掩码提取全部闭合轮廓（裂缝追踪）。
 * 返回的轮廓顶点为整数格点坐标；外边界 signedArea2 < 0，孔洞 > 0。
 */
export function traceContours(mask: Mask): Contour[] {
  checkMask(mask);
  const edges = buildEdges(mask);
  const stride = mask.width + 1;
  const outOf: Map<number, number[]> = new Map();
  for (let i = 0; i < edges.length; i++) {
    const key = edges[i].sy * stride + edges[i].sx;
    const list = outOf.get(key);
    if (list === undefined) outOf.set(key, [i]);
    else list.push(i);
  }
  const visited = new Uint8Array(edges.length);
  const contours: Contour[] = [];
  for (let start = 0; start < edges.length; start++) {
    if (visited[start] === 1) continue;
    const vertices: Point[] = [];
    let cursor = start;
    while (visited[cursor] === 0) {
      visited[cursor] = 1;
      vertices.push({ x: edges[cursor].sx, y: edges[cursor].sy });
      cursor = nextEdgeIndex(edges, outOf, stride, cursor);
    }
    if (cursor !== start) {
      throw new Error('轮廓未闭合：追踪进入了其他环');
    }
    const area2 = signedArea2(vertices);
    contours.push({ vertices, signedArea2: area2, isHole: area2 > 0 });
  }
  return contours;
}
