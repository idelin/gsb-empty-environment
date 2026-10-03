/**
 * 运行样例：时空栅格洪水蔓延模拟。
 *
 * 场景：30x30 栅格（单元 20m），一条南北向河谷贯穿中部，
 * 谷地东侧有一条不透水城区，两条道路横穿河谷。
 * 注入 12 tick 的暴雨时序（前强后弱），逐 tick 输出淹没统计，
 * 并在若干历史时刻输出连通区域边界多边形与道路断点列表。
 */
import {
  FloodSimulator,
  demFromArray,
  createGrid,
  floodedRegions,
  findRoadBreakpoints,
  signedArea,
  type Road,
} from '../src/index.ts';

const ROWS = 30;
const COLS = 30;
const CELL = 20; // 米

// ---- 1. 构建 DEM：中部河谷（col 13-16 最低），整体北高南低 ----
const elev: number[][] = [];
for (let r = 0; r < ROWS; r++) {
  const row: number[] = [];
  for (let c = 0; c < COLS; c++) {
    const distToChannel = Math.max(0, Math.abs(c - 14.5) - 1.5);
    const base = 12 - r * 0.15; // 北高南低
    row.push(base + distToChannel * 1.2);
  }
  elev.push(row);
}
const dem = demFromArray(elev, CELL);

// ---- 2. 渗透率栅格：东侧城区（col>=20）近乎不透水，其余 6mm/tick ----
const infiltration = createGrid(ROWS, COLS, 0.006);
for (let r = 0; r < ROWS; r++) {
  for (let c = 20; c < COLS; c++) infiltration.data[r * COLS + c] = 0.0006;
}

// ---- 3. 暴雨时序：12 tick，峰值为第 4-6 tick ----
const rainfall = { mmPerTick: [4, 8, 15, 25, 35, 30, 18, 10, 5, 2, 0, 0] };

// ---- 4. 道路网：两条东西向道路横穿河谷 ----
const roads: Road[] = [
  { id: '滨河大道', vertices: [{ x: 0, y: 10 * CELL + 10 }, { x: COLS * CELL, y: 10 * CELL + 10 }] },
  {
    id: '环城快速路',
    vertices: [
      { x: 0, y: 22 * CELL },
      { x: 12 * CELL, y: 22 * CELL },
      { x: 18 * CELL, y: 24 * CELL },
      { x: COLS * CELL, y: 24 * CELL },
    ],
  },
];

// ---- 5. 运行仿真 ----
const sim = new FloodSimulator({
  dem,
  infiltration,
  rainfall,
  flowFactor: 0.3,
  floodThreshold: 0.15, // 积水 >15cm 视为淹没
});

console.log('=== 逐 tick 淹没统计 ===');
console.log('tick | 降雨mm | 淹没单元 | 水体体积m³');
for (let t = 0; t < rainfall.mmPerTick.length; t++) {
  sim.step();
  const mask = sim.floodedMaskAt(sim.tick);
  let flooded = 0;
  for (const v of mask) flooded += v;
  console.log(
    `${String(sim.tick).padStart(4)} | ${String(rainfall.mmPerTick[t]).padStart(6)} | ${String(flooded).padStart(8)} | ${sim.totalWaterVolume().toFixed(1)}`,
  );
}

// ---- 6. 任意历史时刻：连通区域边界多边形 ----
for (const t of [4, 8, 12]) {
  const mask = { rows: ROWS, cols: COLS, data: sim.floodedMaskAt(t) };
  const regions = floodedRegions(mask, sim.depthAt(t), CELL);
  console.log(`\n=== tick=${t} 连通淹没区域（共 ${regions.length} 个）===`);
  for (const r of regions) {
    const area = Math.abs(signedArea(r.outerRing));
    console.log(
      `区域#${r.id}: ${r.cellCount} 格, 面积 ${r.areaSqm} m², 多边形面积 ${area.toFixed(0)} m², ` +
        `最大水深 ${r.maxDepth.toFixed(2)} m, 外环顶点 ${r.outerRing.length}, 孔洞 ${r.holes.length}`,
    );
    console.log(`  外环: ${r.outerRing.map((p) => `(${p.x},${p.y})`).join(' -> ')}`);
  }
}

// ---- 7. 任意历史时刻：道路通行断点列表 ----
for (const t of [4, 8, 12]) {
  const bps = findRoadBreakpoints(roads, sim.depthAt(t), t, {
    rows: ROWS,
    cols: COLS,
    cellSize: CELL,
    floodThreshold: 0.15,
  });
  console.log(`\n=== tick=${t} 道路断点（共 ${bps.length} 处）===`);
  for (const bp of bps) {
    console.log(
      `道路[${bp.roadId}] 断点@(${bp.position.x.toFixed(0)},${bp.position.y.toFixed(0)}) ` +
        `单元(${bp.cell.row},${bp.cell.col}) 水深 ${bp.waterDepth.toFixed(2)} m 区域#${bp.regionId}`,
    );
  }
}
