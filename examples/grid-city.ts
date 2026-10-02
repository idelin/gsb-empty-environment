/**
 * Synthetic "urban canyon" city: three parallel east-west avenues
 * (Main / North / South, 50 m apart) connected by cross streets every
 * 200 m. The ground-truth trip drives east on Main St, then turns
 * north at x=400. GPS fixes are corrupted with multipath-like bias.
 */
import { RoadNetwork } from "../src/network.ts";
import type { GpsPoint } from "../src/matcher.ts";
import type { LonLat } from "../src/geometry.ts";

const LAT0 = 31.2;
const LON0 = 121.4;
const M_PER_DEG_LAT = 111320;
const M_PER_DEG_LON = 111320 * Math.cos((LAT0 * Math.PI) / 180);

export function toLonLat(x: number, y: number): LonLat {
  return { lon: LON0 + x / M_PER_DEG_LON, lat: LAT0 + y / M_PER_DEG_LAT };
}

export interface GridCity {
  network: RoadNetwork;
  /** Ground-truth positions (meters in local XY) for each GPS fix. */
  truth: { x: number; y: number }[];
  /** Corrupted GPS observations. */
  trace: GpsPoint[];
  /** Edge ids the true path traverses. */
  trueEdgeIds: Set<string>;
}

/** Deterministic PRNG (mulberry32) so runs are reproducible. */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function gaussian(rand: () => number): number {
  const u = Math.max(rand(), 1e-12);
  const v = rand();
  return Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * v);
}

export function buildGridCity(seed = 42): GridCity {
  const net = new RoadNetwork();
  const nodeIndex = new Map<string, number>();
  const node = (x: number, y: number): number => {
    const key = `${x},${y}`;
    const existing = nodeIndex.get(key);
    if (existing !== undefined) return existing;
    const idx = net.addNode(toLonLat(x, y));
    nodeIndex.set(key, idx);
    return idx;
  };
  const twoWay = (id: string, x1: number, y1: number, x2: number, y2: number) => {
    net.addEdge(id, node(x1, y1), node(x2, y2));
    net.addEdge(`${id}#rev`, node(x2, y2), node(x1, y1));
  };

  // Three east-west avenues, segmented every 100 m.
  const avenues: [string, number][] = [
    ["main", 0],
    ["north", 50],
    ["south", -50],
  ];
  for (const [name, y] of avenues) {
    for (let x = 0; x < 600; x += 100) {
      twoWay(`${name}-${x / 100}`, x, y, x + 100, y);
    }
  }
  // North-south cross streets every 200 m (split at Main St).
  for (const x of [0, 200, 400, 600]) {
    twoWay(`cross-${x}-s`, x, -50, x, 0);
    twoWay(`cross-${x}-n`, x, 0, x, 50);
  }

  // Ground truth: east on Main (0,0)->(400,0), then north to (400,50).
  const truth: { x: number; y: number }[] = [];
  for (let x = 0; x <= 400; x += 50) truth.push({ x, y: 0 });
  truth.push({ x: 400, y: 50 });

  // Multipath corruption: fixes 3..6 sit in the "canyon" and are
  // biased 35-55 m toward North Ave plus heavy jitter.
  const rand = mulberry32(seed);
  const baseSigma = 6;
  const trace: GpsPoint[] = truth.map((p, i) => {
    let nx = gaussian(rand) * baseSigma;
    let ny = gaussian(rand) * baseSigma;
    if (i >= 3 && i <= 6) {
      ny += 35 + rand() * 20; // reflected-signal bias toward North Ave
      nx += gaussian(rand) * 12;
    }
    return {
      point: toLonLat(p.x + nx, p.y + ny),
      timestamp: 1700000000000 + i * 5000, // 5 s sampling, 10 m/s
    };
  });

  const trueEdgeIds = new Set([
    "main-0",
    "main-1",
    "main-2",
    "main-3",
    "cross-400-n",
  ]);
  return { network: net, truth, trace, trueEdgeIds };
}
