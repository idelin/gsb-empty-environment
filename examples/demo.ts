/**
 * Demo: index drone cruise trajectories in a 4D spacetime BVH, then answer
 * the acceptance query:
 *
 *   "all trajectory fragments between 14:00 and 14:10, above 100 m altitude,
 *    crossing the core protected zone"
 *
 * Run with: npm run demo   (requires Node >= 22.6)
 */
import { AABB4, SpacetimeBVH } from '../src/index.ts';
import type { QueryStats, TrajectoryPoint } from '../src/index.ts';

// --- Scenario ------------------------------------------------------------
// Core protected zone: a 2 km x 2 km x (100 m .. 500 m) airspace column
// centred on (0, 0). Time window: 14:00:00 - 14:10:00 local time.
const DAY = '2026-10-03';
const T_1400 = new Date(`${DAY}T14:00:00`).getTime();
const T_1410 = new Date(`${DAY}T14:10:00`).getTime();

const protectedZone = new AABB4(
  -1000, -1000, 100, T_1400,   // min x, y, z(=100 m), t
  1000, 1000, 500, T_1410,     // max x, y, z, t
);

// --- Synthetic drone fleet ------------------------------------------------
// 500 drones cruising for one hour (13:30 - 14:30), one sample per second.
// A few of them are routed straight through the protected zone above 100 m.
function makeDroneTrack(id: number): TrajectoryPoint[] {
  const points: TrajectoryPoint[] = [];
  const start = new Date(`${DAY}T13:30:00`).getTime();
  const throughZone = id % 37 === 0;        // ~14 drones cross the zone
  const lowAltitude = id % 11 === 0;        // some stay below 100 m
  for (let s = 0; s <= 3600; s++) {
    const t = start + s * 1000;
    let x: number;
    let y: number;
    if (throughZone) {
      // Straight west->east cruise passing through the zone centre.
      x = -5000 + (s / 3600) * 10000;
      y = 200 * Math.sin(s / 300) + (id % 5) * 50;
    } else {
      // Orbit far away from the protected zone.
      const angle = s / 600 + id;
      x = 8000 * Math.cos(angle) + (id % 7) * 300;
      y = 8000 * Math.sin(angle) - (id % 5) * 400;
    }
    const z = lowAltitude
      ? 40 + 20 * Math.sin(s / 120)                    // 20 - 60 m
      : 150 + 80 * Math.sin(s / 240 + id);             // 70 - 230 m
    points.push({ x, y, z, t });
  }
  return points;
}

// --- Build the index -------------------------------------------------------
const bvh = new SpacetimeBVH();
const DRONES = 500;

// Bulk-load the whole fleet (balanced tree, fastest construction).
const buildStart = performance.now();
const allSegments = [];
for (let id = 0; id < DRONES; id++) {
  const track = makeDroneTrack(id);
  for (let i = 0; i + 1 < track.length; i++) {
    allSegments.push({
      trajectoryId: `drone-${id}`,
      segmentIndex: i,
      p0: track[i],
      p1: track[i + 1],
      aabb: AABB4.fromPoints(track[i], track[i + 1]),
    });
  }
}
bvh.build(allSegments);
const buildMs = performance.now() - buildStart;

console.log(`Bulk-built index: ${DRONES} trajectories / ${allSegments.length} segments `
  + `in ${buildMs.toFixed(1)} ms (tree depth: ${bvh.depth()})`);

// Incremental insert: a late drone takes off after the index was built.
const insertStart = performance.now();
const lateSegments = bvh.insertTrajectory('drone-late', makeDroneTrack(0));
const insertMs = performance.now() - insertStart;
console.log(`Incrementally inserted ${lateSegments.length} segments of a late drone `
  + `in ${insertMs.toFixed(1)} ms (tree size now ${bvh.size})`);

// --- Acceptance query ------------------------------------------------------
const stats: QueryStats = { nodesVisited: 0, nodesPruned: 0, segmentsTested: 0 };
const queryStart = performance.now();
const fragments = bvh.queryFragments(protectedZone, { stats });
const queryMs = performance.now() - queryStart;

console.log(`\nQuery: 14:00-14:10, altitude >= 100 m, inside protected zone`);
console.log(`Query time: ${queryMs.toFixed(3)} ms | nodes visited: ${stats.nodesVisited}, `
  + `subtrees pruned: ${stats.nodesPruned}, segments tested: ${stats.segmentsTested}`);
console.log(`Matching fragments: ${fragments.length}\n`);

const fmtT = (t: number) => new Date(t).toTimeString().slice(0, 8);
for (const f of fragments.slice(0, 10)) {
  const seg = f.segment;
  console.log(
    `${seg.trajectoryId} seg#${seg.segmentIndex}: `
    + `enter (${f.enter.x.toFixed(0)}, ${f.enter.y.toFixed(0)}, ${f.enter.z.toFixed(0)} m) @ ${fmtT(f.enter.t)}`
    + ` -> exit (${f.exit.x.toFixed(0)}, ${f.exit.y.toFixed(0)}, ${f.exit.z.toFixed(0)} m) @ ${fmtT(f.exit.t)}`,
  );
}
if (fragments.length > 10) console.log(`... and ${fragments.length - 10} more`);

const drones = new Set(fragments.map((f) => f.segment.trajectoryId));
console.log(`\nDistinct drones inside zone: ${drones.size}`);
