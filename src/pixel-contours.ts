/**
 * pixel-contours —— 从二值掩码提取闭合像素轮廓。
 *
 * 算法：像素边（crack）跟随。
 * - 前景采用四邻域、背景采用八邻域（二者互为对偶，保证边界无歧义）。
 * - 轮廓顶点使用整数像素边坐标：像素 (x, y) 占据 [x, x+1] × [y, y+1]。
 * - 有向边界边始终保持“前景在左手侧”，因此外边界在图像坐标系
 *   （y 向下）中为顺时针，孔洞为逆时针。
 * - 在每个顶点按“最左转弯优先”选择后继边：对角接触的前景像素被分为
 *   独立轮廓，对角接触的背景保持连通（不会产生伪孔洞）。
 * - 层级判定与掩码重建全部使用整数射线法，不依赖任何浮点点内判断。
 */

export type MaskData = Uint8Array | number[];

export interface Mask {
  width: number;
  height: number;
  /** 行优先，0 表示背景，非 0 表示前景 */
  data: MaskData;
}

/** 整数像素边坐标（像素角点） */
export type Point = [number, number];

export type Orientation = 'outer' | 'hole';

export interface Contour {
  id: number;
  /** 闭合环的拐角顶点（不重复首点；相邻点沿水平/竖直边相连） */
  points: Point[];
  orientation: Orientation;
  /** 有向面积的两倍：外边界为负，孔洞为正 */
  twiceArea: number;
  /** 父轮廓 id；根（最外层外边界）为 null */
  parent: number | null;
  children: number[];
  /** 嵌套深度：最外层外边界为 0，每深入一层加 1 */
  depth: number;
  /** 轮廓内部紧邻边界的种子像素（外边界为前景像素，孔洞为背景像素） */
  seedX: number;
  seedY: number;
}

export interface ExtractResult {
  width: number;
  height: number;
  contours: Contour[];
}

/* 方向编号（图像坐标系，y 向下）：0=东 1=南 2=西 3=北 */
const E = 0;
const S = 1;
const W = 2;
const N = 3;

export function extractContours(mask: Mask): ExtractResult {
  const width = mask.width;
  const height = mask.height;
  const data = mask.data;
  const vw = width + 1;
  const vh = height + 1;

  function isFg(x: number, y: number): boolean {
    if (x < 0 || y < 0 || x >= width || y >= height) return false;
    return data[y * width + x] !== 0;
  }

  /* 顶点 id = y * vw + x；边 key = 顶点 id * 4 + 方向（方向即边的走向） */
  const outEdges: (number[] | undefined)[] = new Array(vw * vh);
  const used = new Uint8Array(vw * vh * 4);
  const vDelta: number[] = [1, vw, -1, -vw];

  function addEdge(x: number, y: number, dir: number): void {
    const v = y * vw + x;
    let list = outEdges[v];
    if (list === undefined) {
      list = [];
      outEdges[v] = list;
    }
    list.push(v * 4 + dir);
  }

  /* 为每个前景像素生成与背景相邻的有向边（前景始终在左手侧） */
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      if (!isFg(x, y)) continue;
      if (!isFg(x, y - 1)) addEdge(x + 1, y, W);     /* 上边：自东向西 */
      if (!isFg(x, y + 1)) addEdge(x, y + 1, E);     /* 下边：自西向东 */
      if (!isFg(x - 1, y)) addEdge(x, y, S);         /* 左边：自北向南 */
      if (!isFg(x + 1, y)) addEdge(x + 1, y + 1, N); /* 右边：自南向北 */
    }
  }

  const contours: Contour[] = [];
  const edgeSlots = vw * vh * 4;

  for (let start = 0; start < edgeSlots; start++) {
    if (used[start] !== 0) continue;
    const startList = outEdges[start >> 2];
    if (startList === undefined || startList.indexOf(start) === -1) continue;

    /* 沿确定性的后继关系走一整圈 */
    const ring: number[] = [];
    let edge = start;
    while (true) {
      used[edge] = 1;
      const v = edge >> 2;
      const d = edge & 3;
      ring.push(v);
      const endV = v + vDelta[d];
      const outs = outEdges[endV];
      if (outs === undefined) throw new Error('边界不闭合：顶点缺少出边');
      let next = -1;
      for (let k = 0; k < 3; k++) {
        /* 最左转弯优先，其次直行，最后右转（不可能出现掉头边） */
        const candDir = (d + 3 + k) % 4;
        const cand = endV * 4 + candDir;
        if (outs.indexOf(cand) !== -1) {
          next = cand;
          break;
        }
      }
      if (next === -1) throw new Error('边界不闭合：找不到后继边');
      if (next === start) break;
      edge = next;
    }

    /* 顶点 id 转坐标，并压缩共线点（只保留拐角） */
    const n = ring.length;
    const raw: Point[] = [];
    for (let i = 0; i < n; i++) {
      raw.push([ring[i] % vw, Math.floor(ring[i] / vw)]);
    }
    const points: Point[] = [];
    for (let i = 0; i < n; i++) {
      const prev = raw[(i + n - 1) % n];
      const cur = raw[i];
      const nxt = raw[(i + 1) % n];
      const cross =
        (cur[0] - prev[0]) * (nxt[1] - cur[1]) -
        (cur[1] - prev[1]) * (nxt[0] - cur[0]);
      if (cross !== 0) points.push(cur);
    }

    let twiceArea = 0;
    for (let i = 0; i < points.length; i++) {
      const a = points[i];
      const b = points[(i + 1) % points.length];
      twiceArea += a[0] * b[1] - b[0] * a[1];
    }
    const orientation: Orientation = twiceArea < 0 ? 'outer' : 'hole';

    /* 内部种子像素：第一条有向边的内侧相邻像素 */
    const p0 = points[0];
    const p1 = points[1 % points.length];
    let seedX = 0;
    let seedY = 0;
    if (p1[0] > p0[0]) {
      seedX = p0[0];
      seedY = orientation === 'outer' ? p0[1] - 1 : p0[1];
    } else if (p1[0] < p0[0]) {
      seedX = p0[0] - 1;
      seedY = orientation === 'outer' ? p0[1] : p0[1] - 1;
    } else if (p1[1] > p0[1]) {
      seedX = orientation === 'outer' ? p0[0] : p0[0] - 1;
      seedY = p0[1];
    } else {
      seedX = orientation === 'outer' ? p0[0] - 1 : p0[0];
      seedY = p0[1] - 1;
    }

    contours.push({
      id: contours.length,
      points: points,
      orientation: orientation,
      twiceArea: twiceArea,
      parent: null,
      children: [],
      depth: 0,
      seedX: seedX,
      seedY: seedY,
    });
  }

  /* 层级：种子像素被哪些反方向轮廓包含，面积最小者即为直接父轮廓 */
  for (const c of contours) {
    let parent: Contour | null = null;
    for (const k of contours) {
      if (k === c || k.orientation === c.orientation) continue;
      if (!ringContains(k.points, c.seedX, c.seedY)) continue;
      if (parent === null || Math.abs(k.twiceArea) < Math.abs(parent.twiceArea)) {
        parent = k;
      }
    }
    if (parent !== null) {
      c.parent = parent.id;
      parent.children.push(c.id);
    }
  }

  /* 深度：从根（parent 为 null 的外边界）向下传播 */
  const stack: Contour[] = [];
  for (const c of contours) {
    if (c.parent === null) stack.push(c);
  }
  while (stack.length > 0) {
    const cur = stack[stack.length - 1];
    stack.length = stack.length - 1;
    for (const childId of cur.children) {
      const child = contours[childId];
      child.depth = cur.depth + 1;
      stack.push(child);
    }
  }

  return { width: width, height: height, contours: contours };
}

/**
 * 整数射线法：判断像素中心 (px + 0.5, py + 0.5) 是否在环内。
 * 顶点坐标为整数、测试点为半整数，水平射线不可能碰到顶点，
 * 全部比较在整数域精确完成。
 */
function ringContains(points: Point[], px: number, py: number): boolean {
  let inside = false;
  for (let i = 0; i < points.length; i++) {
    const a = points[i];
    const b = points[(i + 1) % points.length];
    if (a[0] !== b[0]) continue; /* 只统计竖直边 */
    const lo = a[1] < b[1] ? a[1] : b[1];
    const hi = a[1] > b[1] ? a[1] : b[1];
    if (a[0] > px && lo <= py && py < hi) inside = !inside;
  }
  return inside;
}

/**
 * 由轮廓重建二值掩码（奇偶填充：外边界与孔洞交替抵消）。
 * 可用于核对提取结果：maskFromContours(extractContours(m)) 应与原掩码一致。
 */
export function maskFromContours(
  width: number,
  height: number,
  contours: Contour[]
): Uint8Array {
  const data = new Uint8Array(width * height);
  for (let y = 0; y < height; y++) {
    const crossings: number[] = [];
    for (const c of contours) {
      const pts = c.points;
      for (let i = 0; i < pts.length; i++) {
        const a = pts[i];
        const b = pts[(i + 1) % pts.length];
        if (a[0] !== b[0]) continue;
        const lo = a[1] < b[1] ? a[1] : b[1];
        const hi = a[1] > b[1] ? a[1] : b[1];
        if (lo <= y && y < hi) crossings.push(a[0]);
      }
    }
    crossings.sort((m, n) => m - n);
    let ci = 0;
    let inside = false;
    for (let x = 0; x < width; x++) {
      while (ci < crossings.length && crossings[ci] <= x) {
        inside = !inside;
        ci++;
      }
      if (inside) data[y * width + x] = 1;
    }
  }
  return data;
}
