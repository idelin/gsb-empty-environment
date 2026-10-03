import { Dem } from "../src/dem.ts";
import { RainfallSeries } from "../src/rainfall.ts";
import { FloodSimulator } from "../src/simulator.ts";
import { analyzeMask } from "../src/regions.ts";
import { RoadNetwork } from "../src/roads.ts";
import { createGrid } from "../src/types.ts";

// --- Scenario -------------------------------------------------------------
// 16x16 catchment, 10 m cells. A valley runs north-south near column 7-8,
// flanked by ridges; elevation drops slightly towards the south (outlet).

const ROWS = 16;
const COLS = 16;
const CELL_SIZE = 10;

const elevation = createGrid(ROWS, COLS, 0);
for (let r = 0; r < ROWS; r++) {
  for (let c = 0; c < COLS; c++) {
    const distToValley = Math.abs(c - 7.5);
    elevation[r][c] = distToValley * 1.2 + (ROWS - 1 - r) * 0.15;
  }
}
const dem = new Dem(elevation, CELL_SIZE);

// Soil permeability: valley floor infiltrates slowly (clay), ridges faster.
const infiltration = createGrid(ROWS, COLS, 0);
for (let r = 0; r < ROWS; r++) {
  for (let c = 0; c < COLS; c++) {
    infiltration[r][c] = Math.abs(c - 7.5) < 2 ? 0.3 : 3;
  }
}

// Storm: 8 ticks of heavy rain decaying over time (mm per tick).
const rainfall = RainfallSeries.uniform([8, 12, 15, 15, 10, 6, 3, 1], ROWS, COLS);

// Roads: R1 crosses the valley west-east, R2 follows the valley then exits.
const roads = new RoadNetwork([
  {
    id: "R1-cross-valley",
    waypoints: [
      { x: 0.5, y: 9.5 },
      { x: 15.5, y: 9.5 },
    ],
  },
  {
    id: "R2-valley-road",
    waypoints: [
      { x: 7.5, y: 0.5 },
      { x: 7.5, y: 12.5 },
      { x: 12.5, y: 15.5 },
    ],
  },
]);

const sim = new FloodSimulator(dem, rainfall, infiltration, {
  flowCoeff: 0.3,
  floodThreshold: 2,
  openBoundary: true,
});

// --- Run -------------------------------------------------------------------

const TICKS = 14;
const BLOCK_THRESHOLD = 8; // mm of water that makes a road impassable

console.log("tick | rain(mm) | ponded(mm*cell) | flooded cells | regions");
console.log("-----+----------+-----------------+---------------+--------");
for (let t = 0; t < TICKS; t++) {
  sim.step();
  const mask = sim.floodedMaskAt(sim.tick);
  const regions = analyzeMask(mask);
  const floodedCells = regions.reduce((n, region) => n + region.cells.length, 0);
  const rain = t < rainfall.length ? rainfall.intensityAt(t)[0][0] : 0;
  console.log(
    [
      String(sim.tick).padStart(4),
      String(rain).padStart(8),
      sim.totalDepthAt(sim.tick).toFixed(1).padStart(15),
      String(floodedCells).padStart(13),
      String(regions.length).padStart(8),
    ].join(" |"),
  );
}

// --- Historical queries ----------------------------------------------------

console.log("\n== Flooded regions & boundary polygons at tick 8 ==");
const mask8 = sim.floodedMaskAt(8);
for (const [i, region] of analyzeMask(mask8).entries()) {
  const outer = region.polygons.find((p) => !p.isHole);
  console.log(
    `region #${i}: ${region.cells.length} cells, ` +
      `${region.polygons.length} boundary loop(s), ` +
      `outer loop ${outer?.vertices.length ?? 0} vertices, ` +
      `area ${(outer?.area ?? 0) * CELL_SIZE * CELL_SIZE} m^2`,
  );
  if (outer !== undefined) {
    const pts = outer.vertices
      .map((p) => `(${(p.x * CELL_SIZE).toFixed(0)},${(p.y * CELL_SIZE).toFixed(0)})`)
      .join(" ");
    console.log(`  outer boundary (m): ${pts}`);
  }
}

console.log("\n== Road breakpoints (water-blocked segments) per historical tick ==");
for (const t of [2, 4, 6, 8, 10, 13]) {
  const depth = sim.depthAt(t);
  const segments = roads.breakpointsAt(depth, t, BLOCK_THRESHOLD);
  if (segments.length === 0) {
    console.log(`tick ${String(t).padStart(2)}: all roads passable`);
    continue;
  }
  for (const seg of segments) {
    const first = seg.cells[0];
    const last = seg.cells[seg.cells.length - 1];
    console.log(
      `tick ${String(t).padStart(2)}: ${seg.roadId} BLOCKED ` +
        `cells (${first.row},${first.col})->(${last.row},${last.col}) ` +
        `count=${seg.cells.length} maxDepth=${seg.maxDepth.toFixed(1)}mm`,
    );
  }
}
