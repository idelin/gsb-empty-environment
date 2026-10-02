import {
  haversine,
  interpolateAlong,
  projectToSegment,
  type LonLat,
} from "./geometry.ts";
import type { RoadNetwork } from "./network.ts";

/** A GPS observation. */
export interface GpsPoint {
  point: LonLat;
  /** Epoch milliseconds. */
  timestamp: number;
}

/** A road-network candidate (hidden state) for one observation. */
export interface Candidate {
  edgeIndex: number;
  /** Meters traveled along the edge geometry from its start node. */
  offset: number;
  /** Snapped position on the edge. */
  snapped: LonLat;
  /** Perpendicular distance from the GPS point to the edge, meters. */
  distance: number;
}

export interface MatchResult {
  observation: GpsPoint;
  candidate: Candidate;
  edgeId: string;
  /** Raw GPS drift: distance from observation to its snapped point. */
  snapDistance: number;
}

export interface MatcherOptions {
  /** GPS measurement std-dev (meters). Urban canyons: 15–30. Default 20. */
  sigma?: number;
  /** Transition scale (meters) for route vs. straight-line mismatch. Default 10. */
  beta?: number;
  /** Candidate search radius around each GPS point (meters). Default 80. */
  candidateRadius?: number;
  /** Absolute speed cap (m/s); faster implied moves are forbidden. Default 60. */
  maxSpeed?: number;
}

const SQRT_2PI = Math.sqrt(2 * Math.PI);

/**
 * HMM map matcher.
 *
 * Hidden states: candidate positions on road edges near each GPS fix.
 * Emission:    Gaussian in the perpendicular distance, sigma = GPS error.
 * Transition:  exponential in |routeDistance - linearDistance| (topology
 *              via Dijkstra shortest paths), gated by a speed plausibility
 *              limit. Decoding: Viterbi over the candidate lattice.
 */
export class MapMatcher {
  readonly network: RoadNetwork;
  readonly sigma: number;
  readonly beta: number;
  readonly candidateRadius: number;
  readonly maxSpeed: number;

  constructor(network: RoadNetwork, options: MatcherOptions = {}) {
    this.network = network;
    this.sigma = options.sigma ?? 20;
    this.beta = options.beta ?? 10;
    this.candidateRadius = options.candidateRadius ?? 80;
    this.maxSpeed = options.maxSpeed ?? 60;
  }

  /** Emission log-probability: Gaussian GPS error model. */
  emissionLogProb(distance: number): number {
    const z = distance / this.sigma;
    return -0.5 * z * z - Math.log(this.sigma * SQRT_2PI);
  }

  /** All edge candidates within the search radius of a GPS point. */
  findCandidates(point: LonLat): Candidate[] {
    const out: Candidate[] = [];
    this.network.edges.forEach((edge, edgeIndex) => {
      let bestOffset = 0;
      let bestDist = Infinity;
      let acc = 0;
      for (let i = 1; i < edge.geometry.length; i++) {
        const a = edge.geometry[i - 1];
        const b = edge.geometry[i];
        const proj = projectToSegment(point, a, b);
        const segLen = haversine(a, b);
        if (proj.distance < bestDist) {
          bestDist = proj.distance;
          bestOffset = acc + proj.fraction * segLen;
        }
        acc += segLen;
      }
      if (bestDist <= this.candidateRadius) {
        out.push({
          edgeIndex,
          offset: bestOffset,
          snapped: interpolateAlong(edge.geometry, bestOffset).point,
          distance: bestDist,
        });
      }
    });
    return out;
  }

  /**
   * Route distance along the network between two candidates (meters),
   * or Infinity when topologically unreachable.
   */
  routeDistance(prev: Candidate, curr: Candidate): number {
    const edges = this.network.edges;
    const prevEdge = edges[prev.edgeIndex];
    const currEdge = edges[curr.edgeIndex];
    if (prev.edgeIndex === curr.edgeIndex) {
      // Same directed edge: only forward travel is allowed.
      return curr.offset >= prev.offset
        ? curr.offset - prev.offset
        : Infinity;
    }
    const remaining = prevEdge.length - prev.offset;
    const middle = this.network.shortestPath(prevEdge.to, currEdge.from);
    if (!Number.isFinite(middle)) return Infinity;
    return remaining + middle + curr.offset;
  }

  /**
   * Transition log-probability between consecutive candidates.
   * Exponential decay in |route - linear| distance mismatch; moves
   * implying a speed above maxSpeed are impossible (-Infinity).
   */
  transitionLogProb(
    prev: Candidate,
    curr: Candidate,
    gpsDistance: number,
    dtSeconds: number,
  ): number {
    const route = this.routeDistance(prev, curr);
    if (!Number.isFinite(route)) return -Infinity;
    if (dtSeconds > 0 && route / dtSeconds > this.maxSpeed) return -Infinity;
    return -Math.log(this.beta) - Math.abs(route - gpsDistance) / this.beta;
  }

  /**
   * Viterbi decoding of the most likely road path for a GPS trace.
   * Returns one snapped result per observation.
   */
  match(trace: GpsPoint[]): MatchResult[] {
    if (trace.length === 0) return [];
    const lattice = trace.map((obs) => this.findCandidates(obs.point));
    lattice.forEach((cands, i) => {
      if (cands.length === 0) {
        throw new Error(
          `No road candidate within ${this.candidateRadius} m of observation ${i}`,
        );
      }
    });

    const n = trace.length;
    // logProb[t][c] = best log-probability of any path ending at candidate c at time t.
    const logProb: number[][] = lattice.map((cands, t) =>
      t === 0
        ? cands.map((c) => this.emissionLogProb(c.distance))
        : cands.map(() => -Infinity),
    );
    const back: number[][] = lattice.map((cands) =>
      new Array<number>(cands.length).fill(-1),
    );

    for (let t = 1; t < n; t++) {
      const dtSeconds = (trace[t].timestamp - trace[t - 1].timestamp) / 1000;
      const gpsDist = haversine(trace[t - 1].point, trace[t].point);
      for (let j = 0; j < lattice[t].length; j++) {
        let best = -Infinity;
        let bestK = -1;
        for (let k = 0; k < lattice[t - 1].length; k++) {
          if (logProb[t - 1][k] === -Infinity) continue;
          const trans = this.transitionLogProb(
            lattice[t - 1][k],
            lattice[t][j],
            gpsDist,
            dtSeconds,
          );
          const score = logProb[t - 1][k] + trans;
          if (score > best) {
            best = score;
            bestK = k;
          }
        }
        if (bestK >= 0) {
          logProb[t][j] =
            best + this.emissionLogProb(lattice[t][j].distance);
          back[t][j] = bestK;
        }
      }
    }

    // Backtrace from the best final state.
    if (Math.max(...logProb[n - 1]) === -Infinity) {
      throw new Error(
        "No feasible path: observations are inconsistent with the network " +
          "(check timestamps, maxSpeed, and connectivity)",
      );
    }
    let idx = 0;
    for (let j = 1; j < lattice[n - 1].length; j++) {
      if (logProb[n - 1][j] > logProb[n - 1][idx]) idx = j;
    }
    const path: Candidate[] = new Array(n);
    for (let t = n - 1; t >= 0; t--) {
      path[t] = lattice[t][idx];
      idx = back[t][idx];
    }

    return path.map((candidate, t) => ({
      observation: trace[t],
      candidate,
      edgeId: this.network.edges[candidate.edgeIndex].id,
      snapDistance: candidate.distance,
    }));
  }
}
