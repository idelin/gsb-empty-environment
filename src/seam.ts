/**
 * 接缝求解器：在两幅已对齐图像的重叠区为每个像素选择来源（A 或 B），
 * 最小化 来源代价之和 + 相邻像素来源切换代价之和。
 *
 * 能量函数（二值马尔可夫随机场，次模，可用 s-t 最小割精确求解）：
 *   E(L) = Σ_p D_p(L_p) + Σ_{(p,q)∈N} V_{pq} · [L_p ≠ L_q]
 *
 * 边界上的固定来源约束通过无穷大容量边实现（硬约束）。
 */

import { Dinic } from './maxflow.ts';

/** 像素来源标签：0 = 图像 A，1 = 图像 B */
export type Label = 0 | 1;

export interface FixedConstraint {
  x: number;
  y: number;
  label: Label;
}

export interface SeamProblem {
  /** 重叠区宽度（像素） */
  width: number;
  /** 重叠区高度（像素） */
  height: number;
  /** costA[y][x]：像素 (x,y) 取自图像 A 的代价，必须 ≥ 0 且有限 */
  costA: number[][];
  /** costB[y][x]：像素 (x,y) 取自图像 B 的代价，必须 ≥ 0 且有限 */
  costB: number[][];
  /** switchCostHorizontal[y][x]：(x,y) 与 (x+1,y) 来源不同的代价，形状 [height][width-1]，缺省为 0 */
  switchCostHorizontal?: number[][];
  /** switchCostVertical[y][x]：(x,y) 与 (x,y+1) 来源不同的代价，形状 [height-1][width]，缺省为 0 */
  switchCostVertical?: number[][];
  /** 边界固定来源约束（硬约束） */
  fixed?: FixedConstraint[];
}

export interface CostBreakdown {
  /** 取自 A 的像素来源代价之和 */
  sourceCostA: number;
  /** 取自 B 的像素来源代价之和 */
  sourceCostB: number;
  /** 水平相邻切换代价之和 */
  switchCostHorizontal: number;
  /** 垂直相邻切换代价之和 */
  switchCostVertical: number;
  /** sourceCostA + sourceCostB */
  sourceCost: number;
  /** switchCostHorizontal + switchCostVertical */
  switchCost: number;
  /** sourceCost + switchCost（即能量 E 的最小值） */
  totalCost: number;
}

export interface SeamResult extends CostBreakdown {
  /** labels[y][x]：每个像素的来源 */
  labels: Label[][];
}

export interface SolveOptions {
  /**
   * 并列最优解的取舍：'A' 偏向来源 A，'B' 偏向来源 B，'none' 不加偏置。
   * 无论取值如何，求解器对相同输入始终给出相同输出（确定性）。
   */
  tieBreak?: 'A' | 'B' | 'none';
}

/** 输入非法时抛出的错误（负代价、非有限值、维度不符、约束冲突等） */
export class SeamInputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SeamInputError';
  }
}

interface NormalizedProblem {
  width: number;
  height: number;
  costA: number[][];
  costB: number[][];
  switchH: number[][];
  switchV: number[][];
  fixed: FixedConstraint[];
}

function checkGrid(grid: number[][], height: number, width: number, name: string): void {
  if (!Array.isArray(grid) || grid.length !== height) {
    throw new SeamInputError(`${name} 应有 ${height} 行，实际为 ${Array.isArray(grid) ? grid.length : '非数组'}`);
  }
  for (let y = 0; y < height; y++) {
    const row = grid[y];
    if (!Array.isArray(row) || row.length !== width) {
      throw new SeamInputError(`${name}[${y}] 应有 ${width} 列，实际为 ${Array.isArray(row) ? row.length : '非数组'}`);
    }
    for (let x = 0; x < width; x++) {
      const v = row[x];
      if (typeof v !== 'number' || Number.isNaN(v) || !Number.isFinite(v)) {
        throw new SeamInputError(`${name}[${y}][${x}] 不是有限数：${v}`);
      }
      if (v < 0) {
        throw new SeamInputError(`${name}[${y}][${x}] 为负值：${v}，代价必须 ≥ 0`);
      }
    }
  }
}

function zeros(height: number, width: number): number[][] {
  return Array.from({ length: height }, () => new Array<number>(width).fill(0));
}

function normalize(problem: SeamProblem): NormalizedProblem {
  if (problem === null || typeof problem !== 'object') {
    throw new SeamInputError('problem 必须是对象');
  }
  const { width, height } = problem;
  if (!Number.isInteger(width) || width < 0) {
    throw new SeamInputError(`width 必须是非负整数：${width}`);
  }
  if (!Number.isInteger(height) || height < 0) {
    throw new SeamInputError(`height 必须是非负整数：${height}`);
  }

  checkGrid(problem.costA, height, width, 'costA');
  checkGrid(problem.costB, height, width, 'costB');

  const switchH = problem.switchCostHorizontal ?? zeros(height, Math.max(0, width - 1));
  const switchV = problem.switchCostVertical ?? zeros(Math.max(0, height - 1), width);
  checkGrid(switchH, height, Math.max(0, width - 1), 'switchCostHorizontal');
  checkGrid(switchV, Math.max(0, height - 1), width, 'switchCostVertical');

  const fixed = problem.fixed ?? [];
  const seen = new Map<number, Label>();
  for (const c of fixed) {
    if (!Number.isInteger(c.x) || !Number.isInteger(c.y) || c.x < 0 || c.x >= width || c.y < 0 || c.y >= height) {
      throw new SeamInputError(`固定约束坐标越界：(${c.x}, ${c.y})，重叠区为 ${width}×${height}`);
    }
    if (c.label !== 0 && c.label !== 1) {
      throw new SeamInputError(`固定约束标签必须是 0(A) 或 1(B)：${c.label}`);
    }
    const key = c.y * width + c.x;
    const prev = seen.get(key);
    if (prev !== undefined && prev !== c.label) {
      throw new SeamInputError(`像素 (${c.x}, ${c.y}) 存在互相冲突的固定约束`);
    }
    seen.set(key, c.label);
  }

  return { width, height, costA: problem.costA, costB: problem.costB, switchH, switchV, fixed };
}

/** 根据标签与原始代价计算费用明细（求解器内部与测试共用的唯一口径） */
export function evaluateCosts(labels: Label[][], problem: SeamProblem): CostBreakdown {
  const p = normalize(problem);
  const height = labels.length;
  const width = height > 0 ? labels[0].length : p.width;
  if (height !== p.height || width !== p.width) {
    throw new SeamInputError(`labels 形状 ${width}×${height} 与重叠区 ${p.width}×${p.height} 不符`);
  }

  let sourceCostA = 0;
  let sourceCostB = 0;
  let switchCostHorizontal = 0;
  let switchCostVertical = 0;

  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const label = labels[y][x];
      if (label === 0) sourceCostA += p.costA[y][x];
      else sourceCostB += p.costB[y][x];
      if (x + 1 < width && label !== labels[y][x + 1]) {
        switchCostHorizontal += p.switchH[y][x];
      }
      if (y + 1 < height && label !== labels[y + 1][x]) {
        switchCostVertical += p.switchV[y][x];
      }
    }
  }

  const sourceCost = sourceCostA + sourceCostB;
  const switchCost = switchCostHorizontal + switchCostVertical;
  return {
    sourceCostA,
    sourceCostB,
    switchCostHorizontal,
    switchCostVertical,
    sourceCost,
    switchCost,
    totalCost: sourceCost + switchCost,
  };
}

/**
 * 求解接缝分割。重叠区为空（width 或 height 为 0）时返回空分割与全零费用。
 * 算法：构造 s-t 网络，源侧对应标签 A，汇侧对应标签 B，Dinic 最大流求最小割。
 */
export function solveSeam(problem: SeamProblem, options: SolveOptions = {}): SeamResult {
  const p = normalize(problem);
  const { width, height } = p;

  if (width === 0 || height === 0) {
    const labels: Label[][] = Array.from({ length: height }, () => []);
    return { labels, ...evaluateCosts(labels, problem) };
  }

  const tieBreak = options.tieBreak ?? 'none';
  if (tieBreak !== 'A' && tieBreak !== 'B' && tieBreak !== 'none') {
    throw new SeamInputError(`未知 tieBreak：${options.tieBreak}`);
  }
  // 微小偏置仅用于打破并列，量级远小于任何真实代价差，不影响非并列情形的最优性
  const eps = tieBreak === 'none' ? 0 : 1e-9;

  const SOURCE = 0;
  const SINK = 1;
  const nodeId = (x: number, y: number): number => 2 + y * width + x;
  const flow = new Dinic(2 + width * height);

  // 无穷大容量：大于任何可行割的有限上界
  let costSum = 0;
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) costSum += p.costA[y][x] + p.costB[y][x];
    for (let x = 0; x + 1 < width; x++) costSum += p.switchH[y][x];
  }
  for (let y = 0; y + 1 < height; y++) {
    for (let x = 0; x < width; x++) costSum += p.switchV[y][x];
  }
  const INF = costSum + 1 + eps * width * height;

  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const id = nodeId(x, y);
      // 割边 SOURCE→p 当且仅当 p 在汇侧（标签 B）；割边 p→SINK 当且仅当 p 在源侧（标签 A）。
      // tieBreak 通过给对立标签加微小偏置来打破并列。
      flow.addEdge(SOURCE, id, p.costB[y][x] + (tieBreak === 'A' ? eps : 0));
      flow.addEdge(id, SINK, p.costA[y][x] + (tieBreak === 'B' ? eps : 0));
      if (x + 1 < width) {
        const w = p.switchH[y][x];
        flow.addEdge(id, nodeId(x + 1, y), w);
        flow.addEdge(nodeId(x + 1, y), id, w);
      }
      if (y + 1 < height) {
        const w = p.switchV[y][x];
        flow.addEdge(id, nodeId(x, y + 1), w);
        flow.addEdge(nodeId(x, y + 1), id, w);
      }
    }
  }

  for (const c of p.fixed) {
    const id = nodeId(c.x, c.y);
    if (c.label === 0) flow.addEdge(SOURCE, id, INF); // p 落在汇侧（B）则割断 INF，强制为 A
    else flow.addEdge(id, SINK, INF); // p 落在源侧（A）则割断 INF，强制为 B
  }

  flow.maxFlow(SOURCE, SINK);
  const sourceSide = flow.reachableFrom(SOURCE);

  const labels: Label[][] = [];
  for (let y = 0; y < height; y++) {
    const row: Label[] = [];
    for (let x = 0; x < width; x++) {
      row.push(sourceSide[nodeId(x, y)] ? 0 : 1);
    }
    labels.push(row);
  }

  // 费用明细按原始（未加偏置）代价重新核算
  return { labels, ...evaluateCosts(labels, problem) };
}
