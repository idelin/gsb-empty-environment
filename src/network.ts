import { haversine, polylineLength, type LonLat } from "./geometry.ts";

export interface RoadEdge {
  id: string;
  /** Index of the start node. */
  from: number;
  /** Index of the end node. */
  to: number;
  /** Polyline geometry (first/last points coincide with the nodes). */
  geometry: LonLat[];
  /** Length of the geometry in meters. */
  length: number;
  /** Speed limit in m/s (defaults to 50 km/h). */
  maxSpeed: number;
}

interface Arc {
  edgeIndex: number;
  to: number;
  weight: number;
}

/**
 * Directed road-network graph. Edges are directed; add both directions
 * for two-way streets. Provides Dijkstra shortest-path distances used
 * by the HMM transition model.
 */
export class RoadNetwork {
  readonly nodes: LonLat[] = [];
  readonly edges: RoadEdge[] = [];
  private adjacency: Arc[][] = [];

  addNode(point: LonLat): number {
    this.nodes.push(point);
    this.adjacency.push([]);
    return this.nodes.length - 1;
  }

  addEdge(
    id: string,
    from: number,
    to: number,
    geometry?: LonLat[],
    maxSpeed = 50 / 3.6,
  ): RoadEdge {
    const geom = geometry ?? [this.nodes[from], this.nodes[to]];
    const edge: RoadEdge = {
      id,
      from,
      to,
      geometry: geom,
      length: polylineLength(geom),
      maxSpeed,
    };
    this.edges.push(edge);
    this.adjacency[from].push({
      edgeIndex: this.edges.length - 1,
      to,
      weight: edge.length,
    });
    return edge;
  }

  /**
   * Shortest-path distance in meters between two nodes (Dijkstra).
   * Returns Infinity when unreachable.
   */
  shortestPath(from: number, to: number): number {
    if (from === to) return 0;
    const dist = new Array<number>(this.nodes.length).fill(Infinity);
    const visited = new Array<boolean>(this.nodes.length).fill(false);
    dist[from] = 0;
    for (;;) {
      let u = -1;
      let best = Infinity;
      for (let i = 0; i < dist.length; i++) {
        if (!visited[i] && dist[i] < best) {
          best = dist[i];
          u = i;
        }
      }
      if (u === -1) break;
      if (u === to) return dist[u];
      visited[u] = true;
      for (const arc of this.adjacency[u]) {
        const nd = dist[u] + arc.weight;
        if (nd < dist[arc.to]) dist[arc.to] = nd;
      }
    }
    return Infinity;
  }

  /** Great-circle (straight line) distance between two nodes. */
  nodeDistance(a: number, b: number): number {
    return haversine(this.nodes[a], this.nodes[b]);
  }
}
