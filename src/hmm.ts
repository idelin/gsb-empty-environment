import type { Candidate } from './types.ts';

export interface HmmParams {
  /** GPS 测量误差标准差（米），用于发射概率的高斯模型。 */
  sigma: number;
  /** 转移概率指数衰减参数 β（米）：|路距 - 直线距| 的容忍尺度。 */
  beta: number;
  /** 最大合理行驶速度（米/秒），超过则转移不可能（纠正瞬移漂移点）。 */
  maxSpeedMps: number;
}

/**
 * 发射概率（对数）：GPS 观测误差服从零均值高斯分布，
 * p(z|r) ∝ exp(-0.5 * (d/σ)²)，d 为观测点到候选路段投影的距离。
 */
export function emissionLogProb(distanceM: number, sigma: number): number {
  const z = distanceM / sigma;
  return -0.5 * z * z - Math.log(sigma * Math.sqrt(2 * Math.PI));
}

/**
 * 转移概率（对数），Newson & Krumm (2009) 模型 + 速度合理性约束：
 * p ∝ exp(-|routeDist - linearDist| / β)
 * 其中 routeDist 为沿路网拓扑的最短行驶距离，linearDist 为相邻 GPS 点直线距离。
 * 若 implied speed = routeDist/Δt 超过 maxSpeedMps，或路网不连通，则施加
 * 很大的固定惩罚（而非 -Infinity），保证个别噪声点不会使整链断裂。
 */
export function transitionLogProb(
  routeDistM: number,
  linearDistM: number,
  dtSec: number,
  params: HmmParams,
): number {
  // 超过 maxSpeed 或不连通的转移施加很大的固定惩罚（而非 -Infinity），
  // 保证个别噪声点或地图缺失不会导致整条路径断裂。
  const IMPOSSIBLE_PENALTY = 200;
  if (!Number.isFinite(routeDistM)) return -IMPOSSIBLE_PENALTY;
  if (dtSec > 0 && routeDistM / dtSec > params.maxSpeedMps) {
    return -IMPOSSIBLE_PENALTY - (routeDistM / dtSec - params.maxSpeedMps);
  }
  return -Math.abs(routeDistM - linearDistM) / params.beta;
}

/**
 * Viterbi 算法：在候选路段图上求全局最可能隐状态序列（对数空间 DP）。
 * @param layers 每个时刻的候选集合（含发射对数概率）
 * @param transition (上一层候选, 本层候选, 时刻 t) => 转移对数概率
 * @returns 每层最优候选的下标序列及最优路径对数概率
 */
export function viterbi(
  layers: Candidate[][],
  transition: (from: Candidate, to: Candidate, t: number) => number,
): { indices: number[]; logProb: number } {
  const T = layers.length;
  if (T === 0) return { indices: [], logProb: 0 };

  let delta = layers[0].map((c) => c.emissionLogProb);
  const backptr: number[][] = [];

  for (let t = 1; t < T; t++) {
    const prev = delta;
    const ptr = new Array<number>(layers[t].length).fill(-1);
    delta = layers[t].map((cand, j) => {
      let best = -Infinity;
      let bestI = -1;
      for (let i = 0; i < prev.length; i++) {
        if (prev[i] === -Infinity) continue;
        const score = prev[i] + transition(layers[t - 1][i], cand, t);
        if (score > best) {
          best = score;
          bestI = i;
        }
      }
      ptr[j] = bestI;
      return best === -Infinity ? -Infinity : best + cand.emissionLogProb;
    });
    backptr.push(ptr);
  }

  let last = 0;
  for (let j = 1; j < delta.length; j++) {
    if (delta[j] > delta[last]) last = j;
  }

  const indices = new Array<number>(T);
  indices[T - 1] = last;
  for (let t = T - 1; t > 0; t--) {
    indices[t - 1] = backptr[t - 1][indices[t]];
  }
  return { indices, logProb: delta[last] };
}
