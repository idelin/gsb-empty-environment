import { haversineM, projectToSegment } from './geometry.ts';
import { emissionLogProb, transitionLogProb, viterbi, type HmmParams } from './hmm.ts';
import type { RoadNetwork } from './road-network.ts';
import type { Candidate, GpsPoint, LatLon, MatchedPoint } from './types.ts';

export interface MatchOptions extends Partial<HmmParams> {
  /** 候选路段搜索半径（米）。 */
  candidateRadiusM?: number;
  /** 每个 GPS 点保留的最大候选数。 */
  maxCandidates?: number;
}

export interface MatchResult {
  matched: MatchedPoint[];
  /** 最优路径的总对数概率。 */
  logProb: number;
  /** 每个 GPS 点的候选数（诊断用）。 */
  candidateCounts: number[];
}

const DEFAULTS = {
  sigma: 20,
  beta: 30,
  maxSpeedMps: 35,
  candidateRadiusM: 120,
  maxCandidates: 5,
};

/** 为一个 GPS 观测生成候选隐状态（路段投影 + 发射概率）。 */
export function findCandidates(
  network: RoadNetwork,
  point: LatLon,
  radiusM: number,
  maxCandidates: number,
  sigma: number,
): Candidate[] {
  const all: Candidate[] = [];
  for (const seg of network.segments) {
    const projection = projectToSegment(point, seg.start, seg.end);
    if (projection.distanceM <= radiusM) {
      all.push({
        segmentId: seg.id,
        projection,
        emissionLogProb: emissionLogProb(projection.distanceM, sigma),
      });
    }
  }
  all.sort((a, b) => a.projection.distanceM - b.projection.distanceM);
  return all.slice(0, maxCandidates);
}

/**
 * HMM 地图匹配主入口：
 * 对每个 GPS 点生成候选路段，用 Viterbi 在候选图上求全局最可能路径，
 * 输出吸附到道路中心线的轨迹。
 */
export function matchTrace(
  network: RoadNetwork,
  trace: GpsPoint[],
  options: MatchOptions = {},
): MatchResult {
  const opts = { ...DEFAULTS, ...options };

  const layers: Candidate[][] = trace.map((p) => {
    // 半径内无候选时逐步扩大搜索半径，保证每层至少一个隐状态。
    for (const scale of [1, 2, 4]) {
      const cands = findCandidates(
        network,
        p,
        opts.candidateRadiusM * scale,
        opts.maxCandidates,
        opts.sigma,
      );
      if (cands.length > 0) return cands;
    }
    // 兜底：不限半径取最近的路段，避免空层导致路径断裂。
    return findCandidates(network, p, Infinity, opts.maxCandidates, opts.sigma);
  });

  const transition = (from: Candidate, to: Candidate, t: number): number => {
    const dtSec = (trace[t].timestamp - trace[t - 1].timestamp) / 1000;
    const linearDistM = haversineM(trace[t - 1], trace[t]);
    const routeDistM = network.routeDistance(from, to);
    return transitionLogProb(routeDistM, linearDistM, dtSec, opts);
  };

  const { indices, logProb } = viterbi(layers, transition);

  const matched: MatchedPoint[] = trace.map((p, t) => {
    const cand = layers[t][indices[t]];
    const seg = network.segment(cand.segmentId);
    return {
      original: p,
      matched: cand.projection.point,
      segmentId: seg.id,
      segmentName: seg.name,
      snapDistanceM: cand.projection.distanceM,
    };
  });

  return {
    matched,
    logProb,
    candidateCounts: layers.map((l) => l.length),
  };
}
