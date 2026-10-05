/**
 * exact-rasterize —— 可能自交的闭合折线的精确栅格化。
 *
 * 显式规则规定：
 * - 采样点：像素 (i, j) 覆盖区域 [i, i+1) × [j, j+1)，采样点固定为像素中心 (i+0.5, j+0.5)。
 * - 坐标约定：顶点为整数坐标（像素单位），x 轴向右、y 轴向下；折线隐式闭合（末点连回首点）。
 * - 边界规则：恰好落在多边形边界（边或顶点）上的采样点，在两种填充规则下均判定为内部。
 * - 水平边：不参与射线穿越计数（由半开区间规则自然排除）。
 * - 顶点：射线恰好经过顶点时按半开区间 [yMin, yMax) 计数，保证每个顶点恰好被计算一次，
 *   不会出现重复计数或漏计。
 * - 精确性：所有坐标放大 2 倍后在整数格上计算（像素中心变为奇数整数），交点横坐标以
 *   精确有理数做符号比较，不做任何浮点除法。坐标规模较小时使用 number（仍为精确整数
 *   运算），超过安全界时自动切换为 BigInt，二者结果完全一致。
 * - 填充规则：
 *   - even-odd：自采样点向右的射线与边界的穿越次数为奇数 → 内部；
 *   - non-zero：有向绕数（向上穿越 +1、向下穿越 -1）非零 → 内部。
 * - 输出除两种规则各自的掩码外，还包括 diff：两种规则判定不一致的像素集合。
 */

export interface Point {
  readonly x: number;
  readonly y: number;
}

export type FillRule = 'even-odd' | 'non-zero';

export interface RasterizeOptions {
  /** 画布宽度（像素），正整数。 */
  readonly width: number;
  /** 画布高度（像素），正整数。 */
  readonly height: number;
}

export interface RasterizeResult {
  readonly width: number;
  readonly height: number;
  /** 奇偶规则掩码，行优先，0/1。 */
  readonly evenOdd: Uint8Array;
  /** 非零绕数规则掩码，行优先，0/1。 */
  readonly nonZero: Uint8Array;
  /** 两种规则判定不同的像素（evenOdd XOR nonZero），行优先，0/1。 */
  readonly diff: Uint8Array;
}

export interface SampleClassification {
  /** 采样点是否恰好落在边界上。 */
  readonly onBoundary: boolean;
  /** 向右射线的穿越次数（奇偶规则依据）。 */
  readonly crossingCount: number;
  /** 有向绕数（非零规则依据）。 */
  readonly windingNumber: number;
  readonly insideEvenOdd: boolean;
  readonly insideNonZero: boolean;
}

/** 精确整数运算抽象：number（快速路径）与 BigInt（兜底路径）共享同一套算法。 */
interface Arith<T> {
  from(n: number): T;
  add(a: T, b: T): T;
  sub(a: T, b: T): T;
  mul(a: T, b: T): T;
  sgn(a: T): number;
  lte(a: T, b: T): boolean;
}

const numberArith: Arith<number> = {
  from: (n) => n,
  add: (a, b) => a + b,
  sub: (a, b) => a - b,
  mul: (a, b) => a * b,
  sgn: (a) => (a > 0 ? 1 : a < 0 ? -1 : 0),
  lte: (a, b) => a <= b,
};

const bigIntArith: Arith<bigint> = {
  from: (n) => BigInt(n),
  add: (a, b) => a + b,
  sub: (a, b) => a - b,
  mul: (a, b) => a * b,
  sgn: (a) => (a > 0n ? 1 : a < 0n ? -1 : 0),
  lte: (a, b) => a <= b,
};

/**
 * number 快速路径的安全界：放大 2 倍后的坐标绝对值 S 满足 S <= 2^24 时，
 * 算法中最大的中间量 8*S^2 <= 2^51，远在 IEEE754 精确整数范围 2^53 之内。
 */
const NUMBER_SAFE_BOUND = 1 << 24;

interface Edge<T> {
  readonly ax: T;
  readonly ay: T;
  readonly by: T;
  readonly dx: T;
  readonly dy: T;
  readonly minX: T;
  readonly maxX: T;
  readonly minY: T;
  readonly maxY: T;
  /** dy > 0（沿 y 轴正方向穿越扫描线时绕数 +1）。 */
  readonly upward: boolean;
}

interface CoreResult {
  readonly onBoundary: boolean;
  readonly crossingCount: number;
  readonly windingNumber: number;
}

function validateVertices(vertices: readonly Point[]): void {
  for (const v of vertices) {
    if (!Number.isInteger(v.x) || !Number.isInteger(v.y)) {
      throw new Error(`vertex coordinates must be finite integers, got (${v.x}, ${v.y})`);
    }
  }
}

/** 顶点坐标放大 2 倍，构建边表；零长度边（重复点）直接丢弃。 */
function buildEdges<T>(arith: Arith<T>, vertices: readonly Point[]): Edge<T>[] {
  const edges: Edge<T>[] = [];
  const n = vertices.length;
  for (let i = 0; i < n; i++) {
    const a = vertices[i];
    const b = vertices[(i + 1) % n];
    const ax = arith.from(2 * a.x);
    const ay = arith.from(2 * a.y);
    const bx = arith.from(2 * b.x);
    const by = arith.from(2 * b.y);
    const dx = arith.sub(bx, ax);
    const dy = arith.sub(by, ay);
    if (arith.sgn(dx) === 0 && arith.sgn(dy) === 0) continue;
    edges.push({
      ax,
      ay,
      by,
      dx,
      dy,
      minX: arith.lte(ax, bx) ? ax : bx,
      maxX: arith.lte(ax, bx) ? bx : ax,
      minY: arith.lte(ay, by) ? ay : by,
      maxY: arith.lte(ay, by) ? by : ay,
      upward: arith.sgn(dy) > 0,
    });
  }
  return edges;
}

/**
 * 对放大 2 倍后的采样点 (px, py) 做单点分类。
 * 射线方向为 +x。所有比较均为精确有理数符号判定。
 */
function classifyCore<T>(arith: Arith<T>, edges: readonly Edge<T>[], px: T, py: T): CoreResult {
  let crossingCount = 0;
  let windingNumber = 0;
  for (const e of edges) {
    // 边界判定：cross = dx*(py-ay) - dy*(px-ax)，为零且落在包围盒内即共线在线段上。
    const cross = arith.sub(
      arith.mul(e.dx, arith.sub(py, e.ay)),
      arith.mul(e.dy, arith.sub(px, e.ax)),
    );
    if (
      arith.sgn(cross) === 0 &&
      arith.lte(e.minX, px) && arith.lte(px, e.maxX) &&
      arith.lte(e.minY, py) && arith.lte(py, e.maxY)
    ) {
      return { onBoundary: true, crossingCount, windingNumber };
    }
    // 半开区间 [yMin, yMax)：水平边两端判定相同自然排除；顶点恰好被一条边计入。
    const aOnOrAbove = arith.lte(e.ay, py);
    const bOnOrAbove = arith.lte(e.by, py);
    if (aOnOrAbove !== bOnOrAbove) {
      // 交点横坐标与 px 的有理比较：xInt - px = diff / dy，只需比较符号。
      const diff = arith.add(
        arith.mul(e.dy, arith.sub(e.ax, px)),
        arith.mul(e.dx, arith.sub(py, e.ay)),
      );
      if (arith.sgn(diff) === arith.sgn(e.dy)) {
        crossingCount++;
        windingNumber += e.upward ? 1 : -1;
      }
    }
  }
  return { onBoundary: false, crossingCount, windingNumber };
}

function rasterizeWith<T>(
  arith: Arith<T>,
  vertices: readonly Point[],
  width: number,
  height: number,
): RasterizeResult {
  const edges = buildEdges(arith, vertices);
  const evenOdd = new Uint8Array(width * height);
  const nonZero = new Uint8Array(width * height);
  const diff = new Uint8Array(width * height);
  for (let j = 0; j < height; j++) {
    const py = arith.from(2 * j + 1);
    for (let i = 0; i < width; i++) {
      const px = arith.from(2 * i + 1);
      const c = classifyCore(arith, edges, px, py);
      const eo = c.onBoundary || c.crossingCount % 2 === 1;
      const nz = c.onBoundary || c.windingNumber !== 0;
      const idx = j * width + i;
      evenOdd[idx] = eo ? 1 : 0;
      nonZero[idx] = nz ? 1 : 0;
      diff[idx] = eo !== nz ? 1 : 0;
    }
  }
  return { width, height, evenOdd, nonZero, diff };
}

function maxAbsVertex(vertices: readonly Point[]): number {
  let maxAbs = 0;
  for (const v of vertices) {
    maxAbs = Math.max(maxAbs, Math.abs(v.x), Math.abs(v.y));
  }
  return maxAbs;
}

/**
 * 栅格化一条闭合折线，输出奇偶规则、非零绕数规则及两者差异的像素掩码。
 * 顶点不足或全部退化时返回全零掩码。
 */
export function rasterize(vertices: readonly Point[], options: RasterizeOptions): RasterizeResult {
  const { width, height } = options;
  if (!Number.isInteger(width) || !Number.isInteger(height) || width <= 0 || height <= 0) {
    throw new Error(`width and height must be positive integers, got ${width}x${height}`);
  }
  validateVertices(vertices);
  const maxAbs = Math.max(maxAbsVertex(vertices), width, height);
  if (2 * maxAbs + 1 <= NUMBER_SAFE_BOUND) {
    return rasterizeWith(numberArith, vertices, width, height);
  }
  return rasterizeWith(bigIntArith, vertices, width, height);
}

/**
 * 对单个采样点分类（主要用于测试与调试）。
 * 坐标必须是 0.5 的整数倍；像素 (i, j) 的中心对应 (i+0.5, j+0.5)。
 */
export function classifySample(vertices: readonly Point[], x: number, y: number): SampleClassification {
  validateVertices(vertices);
  if (!Number.isInteger(2 * x) || !Number.isInteger(2 * y)) {
    throw new Error(`sample coordinates must be multiples of 0.5, got (${x}, ${y})`);
  }
  const maxAbs = Math.max(maxAbsVertex(vertices), Math.abs(x), Math.abs(y));
  const c = 2 * maxAbs + 1 <= NUMBER_SAFE_BOUND
    ? classifyCore(numberArith, buildEdges(numberArith, vertices), 2 * x, 2 * y)
    : classifyCore(bigIntArith, buildEdges(bigIntArith, vertices), BigInt(2 * x), BigInt(2 * y));
  return {
    onBoundary: c.onBoundary,
    crossingCount: c.crossingCount,
    windingNumber: c.windingNumber,
    insideEvenOdd: c.onBoundary || c.crossingCount % 2 === 1,
    insideNonZero: c.onBoundary || c.windingNumber !== 0,
  };
}

/** 将掩码渲染为 ASCII 字符串（默认 '#' 填充、'.' 空白），便于调试与测试。 */
export function maskToString(
  mask: Uint8Array,
  width: number,
  height: number,
  on = '#',
  off = '.',
): string {
  const rows: string[] = [];
  for (let j = 0; j < height; j++) {
    let row = '';
    for (let i = 0; i < width; i++) {
      row += mask[j * width + i] ? on : off;
    }
    rows.push(row);
  }
  return rows.join('\n');
}
