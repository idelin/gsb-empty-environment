import { haversineM } from './geometry.ts';
import type { Candidate, LatLon, RoadSegment } from './types.ts';

interface Edge {
  to: number;
  lengthM: number;
  segmentId: number;
}

/**
 * 路网：交叉口为节点、路段为边的无向加权图。
 * 提供候选间“沿路网拓扑的最短路距离”（route distance）。
 */
export class RoadNetwork {
  readonly nodes: LatLon[] = [];
  readonly segments: RoadSegment[] = [];
  private readonly nodeKeys = new Map<string, number>();
  private readonly adj: Edge[][] = [];
  private readonly spCache = new Map<number, number[]>();

  addSegment(start: LatLon, end: LatLon, name: string): RoadSegment {
    const startNode = this.nodeId(start);
    const endNode = this.nodeId(end);
    const seg: RoadSegment = {
      id: this.segments.length,
      name,
      start,
      end,
      lengthM: haversineM(start, end),
      startNode,
      endNode,
    };
    this.segments.push(seg);
    this.adj[startNode].push({ to: endNode, lengthM: seg.lengthM, segmentId: seg.id });
    this.adj[endNode].push({ to: startNode, lengthM: seg.lengthM, segmentId: seg.id });
    this.spCache.clear();
    return seg;
  }

  segment(id: number): RoadSegment {
    return this.segments[id];
  }

  private nodeId(p: LatLon): number {
    const key = `${p.lat.toFixed(7)},${p.lon.toFixed(7)}`;
    const existing = this.nodeKeys.get(key);
    if (existing !== undefined) return existing;
    const id = this.nodes.length;
    this.nodeKeys.set(key, id);
    this.nodes.push(p);
    this.adj.push([]);
    return id;
  }

  /** Dijkstra 单源最短路（按源节点缓存）。 */
  shortestPathsFrom(source: number): number[] {
    const cached = this.spCache.get(source);
    if (cached) return cached;

    const n = this.nodes.length;
    const dist = new Array<number>(n).fill(Infinity);
    const done = new Array<boolean>(n).fill(false);
    dist[source] = 0;
    for (let iter = 0; iter < n; iter++) {
      let u = -1;
      let best = Infinity;
      for (let i = 0; i < n; i++) {
        if (!done[i] && dist[i] < best) {
          best = dist[i];
          u = i;
        }
      }
      if (u < 0) break;
      done[u] = true;
      for (const e of this.adj[u]) {
        const nd = dist[u] + e.lengthM;
        if (nd < dist[e.to]) dist[e.to] = nd;
      }
    }
    this.spCache.set(source, dist);
    return dist;
  }

  /**
   * 两个候选匹配之间沿路网的最短行驶距离（米）。
   * 同一路段上为沿线距离；跨路段时经路段端点走最短路。
   * 不连通时返回 Infinity。
   */
  routeDistance(a: Candidate, b: Candidate): number {
    const segA = this.segments[a.segmentId];
    const segB = this.segments[b.segmentId];
    const alongA = a.projection.alongM;
    const alongB = b.projection.alongM;

    if (segA.id === segB.id) {
      return Math.abs(alongA - alongB);
    }

    const distFromA0 = this.shortestPathsFrom(segA.startNode);
    const distFromA1 = this.shortestPathsFrom(segA.endNode);

    // 从 A 的投影点先走到 A 的某一端，再经最短路到 B 的某一端，最后沿 B 走到投影点。
    const options = [
      alongA + distFromA0[segB.startNode] + alongB,
      alongA + distFromA0[segB.endNode] + (segB.lengthM - alongB),
      (segA.lengthM - alongA) + distFromA1[segB.startNode] + alongB,
      (segA.lengthM - alongA) + distFromA1[segB.endNode] + (segB.lengthM - alongB),
    ];
    return Math.min(...options);
  }
}
