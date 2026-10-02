import { SpaceTimeBVH, queryBox, trajectoryToSegments, type Vec4 } from '../src/index.ts';

/**
 * Demo: drone cruise trajectories over a city.
 *
 * Coordinates: metres relative to city centre (x=east, y=north, z=altitude).
 * Time: epoch ms. The core protection zone is a 2km x 2km ground square,
 * and we only care about altitude >= 100m, between 14:00 and 14:10.
 */

const DAY = '2026-10-02';
const t = (hhmm: string): number => new Date(`${DAY}T${hhmm}:00+08:00`).getTime();

// Core protection zone: x,y in [-1000, 1000] m.
const ZONE = { minX: -1000, maxX: 1000, minY: -1000, maxY: 1000 };
const MIN_ALTITUDE = 100;
const WINDOW = { t1: t('14:00'), t2: t('14:10') };

// --- Build a fleet of drone cruise trajectories (waypoint polylines) ---

const drones: Array<{ id: string; points: Vec4[] }> = [
  {
    // Crosses the zone east->west at 150m right inside the window. HIT.
    id: 'UAV-001',
    points: [
      { x: -3000, y: 200, z: 150, t: t('13:58') },
      { x: -500, y: 100, z: 150, t: t('14:03') },
      { x: 800, y: -50, z: 160, t: t('14:07') },
      { x: 3000, y: -300, z: 160, t: t('14:12') },
    ],
  },
  {
    // Crosses the zone at 150m but only at 14:30, outside the window. MISS (time).
    id: 'UAV-002',
    points: [
      { x: -3000, y: 0, z: 150, t: t('14:20') },
      { x: 0, y: 0, z: 150, t: t('14:30') },
      { x: 3000, y: 0, z: 150, t: t('14:40') },
    ],
  },
  {
    // Crosses the zone inside the window but cruises at 60m. MISS (altitude).
    id: 'UAV-003',
    points: [
      { x: -2000, y: 500, z: 60, t: t('14:02') },
      { x: 0, y: 500, z: 60, t: t('14:06') },
      { x: 2000, y: 500, z: 60, t: t('14:09') },
    ],
  },
  {
    // Enters the zone climbing: below 100m at the edge, above 100m inside.
    // The portion of the segment above 100m is inside the zone. HIT.
    id: 'UAV-004',
    points: [
      { x: -1500, y: -800, z: 50, t: t('14:04') },
      { x: -200, y: -100, z: 220, t: t('14:06') },
      { x: 1200, y: 600, z: 250, t: t('14:08') },
    ],
  },
  {
    // High and inside the window, but far north of the zone. MISS (space).
    id: 'UAV-005',
    points: [
      { x: -2000, y: 5000, z: 300, t: t('14:01') },
      { x: 2000, y: 5000, z: 300, t: t('14:09') },
    ],
  },
  {
    // Loitering inside the zone at 120m for the whole afternoon. HIT.
    id: 'UAV-006',
    points: [
      { x: 100, y: 100, z: 120, t: t('13:00') },
      { x: 150, y: 120, z: 120, t: t('15:00') },
    ],
  },
];

// --- Index every trajectory leg in the 4D BVH ---

const allSegments = drones.flatMap((d) => trajectoryToSegments(d.id, d.points));
const bvh = new SpaceTimeBVH(allSegments);

console.log(`Indexed ${allSegments.length} trajectory segments from ${drones.length} drones`);
console.log(`BVH nodes: ${bvh.nodeCount()}\n`);

// --- Query: 14:00-14:10, inside protection zone, altitude >= 100m ---

const box = queryBox(
  WINDOW.t1, WINDOW.t2,
  ZONE.minX, ZONE.maxX,
  ZONE.minY, ZONE.maxY,
  MIN_ALTITUDE, Infinity,
);

const { segments, stats } = bvh.query(box);

console.log(
  `Query: 14:00-14:10, zone x/y in [-1000,1000]m, z >= ${MIN_ALTITUDE}m`,
);
console.log(
  `Visited ${stats.nodesVisited}/${bvh.nodeCount()} nodes, ` +
  `tested ${stats.candidatesTested}/${allSegments.length} segments, ` +
  `${stats.elapsedMs.toFixed(3)} ms\n`,
);

for (const seg of segments) {
  const fmt = (ms: number): string =>
    new Date(ms).toLocaleTimeString('zh-CN', { hour12: false, timeZone: 'Asia/Shanghai' });
  console.log(
    `HIT ${seg.trajectoryId} seg#${seg.segmentIndex}: ` +
    `(${seg.p0.x},${seg.p0.y},${seg.p0.z})@${fmt(seg.p0.t)} -> ` +
    `(${seg.p1.x},${seg.p1.y},${seg.p1.z})@${fmt(seg.p1.t)}`,
  );
}
