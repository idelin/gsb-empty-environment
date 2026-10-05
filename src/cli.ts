import { readFileSync } from "node:fs";
import { DEPOT, simulate } from "./simulate.ts";
import { effectiveMode, solve } from "./solver.ts";
import type { Scenario } from "./types.ts";

const scenarioPath = process.argv[2] ?? "scenarios/example.json";
const sc: Scenario = JSON.parse(readFileSync(scenarioPath, "utf8"));

console.log(`场景: ${sc.name ?? scenarioPath}`);
console.log(
  `站点 ${sc.stations.length} 个 · 载重上限 ${sc.vehicle.capacity} · ` +
    `里程成本 ${sc.costs.perKm}/km · 缺车罚金 ${sc.costs.shortagePerBike}/辆 · ` +
    `模式 ${effectiveMode(sc)}`,
);

const t0 = Date.now();
const plan = solve(sc);
const { rows } = simulate(sc, plan.moves);
const elapsed = Date.now() - t0;

const locName = (loc: number): string => (loc === DEPOT ? "仓库" : sc.stations[loc - 1].name);

console.log("\n=== 最优方案 ===");
console.log(
  `总里程 ${plan.distanceKm.toFixed(2)} km · 里程成本 ${plan.mileageCost.toFixed(2)} · ` +
    `缺车 ${plan.shortageBikes} 辆 · 罚金 ${plan.penaltyCost.toFixed(2)} · ` +
    `总成本 ${plan.totalCost.toFixed(2)}（求解耗时 ${elapsed} ms）`,
);

const actionOf = (loc: number, transfer: number): string => {
  if (loc === DEPOT) {
    if (transfer > 0) return `卸车 ${transfer} 辆`;
    if (transfer < 0) return `装车 ${-transfer} 辆`;
    return "回仓";
  }
  if (transfer > 0) return `放下 ${transfer} 辆`;
  if (transfer < 0) return `取走 ${-transfer} 辆`;
  return "途经";
};

console.log("\n=== 时间线（各站与车载库存） ===");
const stationNames = sc.stations.map((s) => s.name);
const header = ["步骤", "位置", "动作", "车载", ...stationNames, "累计km"];
const widths = header.map((h, i) =>
  Math.max(4, ...rows.map(() => 0), i < 4 ? 6 : 4, h.length * 2),
);
const line = (cells: string[]): string =>
  cells.map((c, i) => c.padStart(widths[i])).join(" ");
console.log(line(header));
for (const r of rows) {
  console.log(
    line([
      String(r.step),
      locName(r.location),
      actionOf(r.location, r.transfer),
      String(r.load),
      ...r.inventories.map((v) => String(v)),
      r.distance.toFixed(1),
    ]),
  );
}

console.log("\n=== 期末库存 vs 次日需求 ===");
for (let i = 0; i < sc.stations.length; i++) {
  const s = sc.stations[i];
  const fin = plan.finalInventories[i];
  const mark = fin < s.target ? `缺 ${s.target - fin}` : fin > s.target ? `多 ${fin - s.target}` : "达标";
  console.log(`  ${s.name}: ${fin} / ${s.target}（${mark}）`);
}
