import type { Contour, ContourNode, ContourSet, Hierarchy, Mask } from './types.js';
import { traceContours } from './extract.js';

/**
 * 精确点内判断：点用二倍坐标 (px2, py2)（奇数），轮廓顶点为整数坐标。
 * 向右射线与竖直边的相交判定全部在整数域完成，不存在浮点退化情形
 * （奇数永远不等于偶数，点不会落在边上或顶点上）。
 */
function pointInContour(px2: number, py2: number, contour: Contour): boolean {
  const vertices = contour.vertices;
  const n = vertices.length;
  let inside = false;
  for (let i = 0; i < n; i++) {
    const a = vertices[i];
    const b = vertices[(i + 1) % n];
    if (a.x !== b.x) continue;
    const y1 = a.y * 2;
    const y2 = b.y * 2;
    if (a.x * 2 > px2 && (y1 < py2) !== (y2 < py2)) inside = !inside;
  }
  return inside;
}

/**
 * 由轮廓集合构建嵌套层级。
 * 代表点取每条轮廓首条边的中点（二倍坐标）：边界无向边唯一属于一条
 * 轮廓，因此该点对其余轮廓必然严格在内或严格在外。
 * 父轮廓取所有包含者中面积最小者；深度奇偶与 isHole 互相校验。
 */
export function buildHierarchy(contours: Contour[]): Hierarchy {
  const nodes: ContourNode[] = contours.map((unused: Contour, index: number) => ({
    index,
    parent: -1,
    children: [],
    depth: 0,
  }));
  for (let i = 0; i < contours.length; i++) {
    const v = contours[i].vertices;
    const px2 = v[0].x + v[1].x;
    const py2 = v[0].y + v[1].y;
    let best = -1;
    let bestArea = 0;
    for (let j = 0; j < contours.length; j++) {
      if (i === j) continue;
      if (!pointInContour(px2, py2, contours[j])) continue;
      const area = contours[j].signedArea2 < 0 ? -contours[j].signedArea2 : contours[j].signedArea2;
      if (best === -1 || area < bestArea) {
        best = j;
        bestArea = area;
      }
    }
    nodes[i].parent = best;
    if (best !== -1) nodes[best].children.push(i);
  }
  const roots: number[] = [];
  const stack: number[] = [];
  for (let i = 0; i < nodes.length; i++) {
    if (nodes[i].parent === -1) {
      nodes[i].depth = 0;
      roots.push(i);
      stack.push(i);
    }
  }
  while (stack.length > 0) {
    const current = stack.pop();
    if (current === undefined) continue;
    for (const child of nodes[current].children) {
      nodes[child].depth = nodes[current].depth + 1;
      stack.push(child);
    }
  }
  for (let i = 0; i < contours.length; i++) {
    if (contours[i].isHole !== (nodes[i].depth % 2 === 1)) {
      throw new Error('层级与轮廓朝向不一致');
    }
  }
  return { nodes, roots };
}

/** 一步提取：轮廓 + 嵌套层级。 */
export function extractContours(mask: Mask): ContourSet {
  const contours = traceContours(mask);
  const hierarchy = buildHierarchy(contours);
  return { contours, nodes: hierarchy.nodes, roots: hierarchy.roots };
}
