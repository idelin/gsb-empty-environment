import { EARTH_RADIUS_M } from './geometry.ts';
import { RoadNetwork } from './road-network.ts';
import type { GpsPoint, LatLon } from './types.ts';

/** 可复现的伪随机数发生器（mulberry32）。 */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Box-Muller 标准正态采样。 */
function gaussian(rng: () => number): number {
  const u1 = Math.max(rng(), 1e-12);
  const u2 = rng();
  return Math.sqrt(-2 * Math.log(u1)) * Math.cos(2 * Math.PI * u2);
}

export interface Scenario {
  network: RoadNetwork;
  /** 受多径效应污染的 GPS 观测序列。 */
  gps: GpsPoint[];
  /** 每个观测对应的真实路段 id（ground truth）。 */
  trueSegmentIds: number[];
  /** 每个观测的真实位置（道路中心线上）。 */
  truePositions: LatLon[];
}

const ORIGIN: LatLon = { lat: 31.2304, lon: 121.4737 }; // 上海人民广场附近
const DEG = Math.PI / 180;

function gridToLatLon(x: number, y: number): LatLon {
  return {
    lat: ORIGIN.lat + y / EARTH_RADIUS_M / DEG,
    lon: ORIGIN.lon + x / (EARTH_RADIUS_M * Math.cos(ORIGIN.lat * DEG)) / DEG,
  };
}

function segKey(x1: number, y1: number, x2: number, y2: number): string {
  return x1 < x2 || (x1 === x2 && y1 < y2)
    ? `${x1},${y1}|${x2},${y2}`
    : `${x2},${y2}|${x1},${y1}`;
}

/**
 * 构造“城市峡谷”场景：
 * - 5x5 交叉口、100m 间距的网格路网（东西向 H0..H4，南北向 V0..V4）；
 * - 真实路径：H0 向东 → 沿 V3 向北（两侧高楼、多径严重）→ H2 向东；
 * - 观测：10 m/s 每 2s 采样，基础噪声 σ=6m；
 *   V3 峡谷段观测以 65% 概率向平行街道（V2/V4）漂移 55~85m，
 *   H0 段混入 2 个向 H1 方向约 85m 的粗大离群点。
 */
export function buildCityCanyonScenario(seed = 42): Scenario {
  const rng = mulberry32(seed);
  const network = new RoadNetwork();
  const segIds = new Map<string, number>();

  const BLOCK = 100;
  const N = 5;
  for (let r = 0; r < N; r++) {
    for (let c = 0; c < N - 1; c++) {
      const seg = network.addSegment(
        gridToLatLon(c * BLOCK, r * BLOCK),
        gridToLatLon((c + 1) * BLOCK, r * BLOCK),
        `H${r}-${c}`,
      );
      segIds.set(segKey(c * BLOCK, r * BLOCK, (c + 1) * BLOCK, r * BLOCK), seg.id);
    }
  }
  for (let c = 0; c < N; c++) {
    for (let r = 0; r < N - 1; r++) {
      const seg = network.addSegment(
        gridToLatLon(c * BLOCK, r * BLOCK),
        gridToLatLon(c * BLOCK, (r + 1) * BLOCK),
        `V${c}-${r}`,
      );
      segIds.set(segKey(c * BLOCK, r * BLOCK, c * BLOCK, (r + 1) * BLOCK), seg.id);
    }
  }

  // 真实路径（米制网格坐标下的折线）。
  const waypoints: [number, number][] = [
    [0, 0],
    [300, 0],
    [300, 200],
    [400, 200],
  ];
  const legs = waypoints.slice(0, -1).map(([x1, y1], i) => {
    const [x2, y2] = waypoints[i + 1];
    return { x1, y1, x2, y2, len: Math.hypot(x2 - x1, y2 - y1) };
  });
  const totalLen = legs.reduce((s, l) => s + l.len, 0);

  const SPEED_MPS = 10;
  const DT_SEC = 2;
  const STEP = SPEED_MPS * DT_SEC; // 20m
  const SIGMA_GPS = 6;

  const gps: GpsPoint[] = [];
  const trueSegmentIds: number[] = [];
  const truePositions: LatLon[] = [];

  // 沿路径每 20m 采样（起点偏移 10m，避开交叉口归属歧义）。
  let sampleIndex = 0;
  for (let s = STEP / 2; s <= totalLen; s += STEP, sampleIndex++) {
    let acc = 0;
    let leg = legs[0];
    for (const l of legs) {
      if (s <= acc + l.len) {
        leg = l;
        break;
      }
      acc += l.len;
    }
    const frac = (s - acc) / leg.len;
    const x = leg.x1 + (leg.x2 - leg.x1) * frac;
    const y = leg.y1 + (leg.y2 - leg.y1) * frac;

    // 真实路段：水平腿取所在格子的东西向段，垂直腿取南北向段。
    const cx = Math.min(Math.floor(x / BLOCK), N - 2);
    const cy = Math.min(Math.floor(y / BLOCK), N - 2);
    const key =
      leg.y1 === leg.y2
        ? segKey(cx * BLOCK, y, (cx + 1) * BLOCK, y)
        : segKey(x, cy * BLOCK, x, (cy + 1) * BLOCK);
    trueSegmentIds.push(segIds.get(key)!);
    truePositions.push(gridToLatLon(x, y));

    // 基础 GPS 噪声。
    let nx = x + SIGMA_GPS * gaussian(rng);
    let ny = y + SIGMA_GPS * gaussian(rng);

    // 城市峡谷多径：V3（x=300）上的观测向平行街道剧烈漂移。
    const inCanyon = leg.x1 === leg.x2 && leg.x1 === 300 && y > 0 && y < 200;
    if (inCanyon) {
      const roll = rng();
      if (roll < 0.4) nx += 55 + 30 * rng(); // 偏向 V4（x=400）
      else if (roll < 0.65) nx -= 55 + 30 * rng(); // 偏向 V2（x=200）
    }

    // H0 上的两个粗大离群点（偏向 H1，y=100）。
    if (sampleIndex === 6 || sampleIndex === 11) {
      nx += 20 * (rng() - 0.5);
      ny += 80 + 10 * rng();
    }

    gps.push({ ...gridToLatLon(nx, ny), timestamp: sampleIndex * DT_SEC * 1000 });
  }

  return { network, gps, trueSegmentIds, truePositions };
}
