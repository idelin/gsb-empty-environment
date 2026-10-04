import { readFileSync } from "node:fs";
import { planPicking } from "./planner.ts";
import type { Plan, Scenario } from "./types.ts";

// Small aisle graph:
//
//            L1   L2          L5
//            |    |           |
//  D -- X -- A1--A2 --cross-- B2 -- B1
//            |    |           |      |
//            L3   L4          L6     L7
export const demoScenario: Scenario = {
  depot: "D",
  capacity: 6,
  splitCost: 8,
  graph: {
    nodes: ["D", "X", "A1", "A2", "B1", "B2", "L1", "L2", "L3", "L4", "L5", "L6", "L7"],
    edges: [
      { from: "D", to: "X", dist: 1 },
      { from: "X", to: "A1", dist: 2 },
      { from: "A1", to: "A2", dist: 2 },
      { from: "X", to: "B1", dist: 2 },
      { from: "B1", to: "B2", dist: 2 },
      { from: "A2", to: "B2", dist: 3 }, // cross aisle
      { from: "A1", to: "L1", dist: 1 },
      { from: "A1", to: "L2", dist: 1 },
      { from: "A2", to: "L3", dist: 1 },
      { from: "A2", to: "L4", dist: 1 },
      { from: "B2", to: "L5", dist: 1 },
      { from: "B2", to: "L6", dist: 1 },
      { from: "B1", to: "L7", dist: 1 },
    ],
  },
  lines: [
    { order: "O1", location: "L1", qty: 2 },
    { order: "O1", location: "L3", qty: 1 },
    { order: "O2", location: "L1", qty: 1 },
    { order: "O2", location: "L5", qty: 2 },
    { order: "O3", location: "L2", qty: 3 },
    { order: "O3", location: "L6", qty: 1 },
    { order: "O4", location: "L4", qty: 2 },
    { order: "O4", location: "L6", qty: 1 },
    { order: "O4", location: "L5", qty: 1 },
  ],
};

export function formatPlan(plan: Plan, scenario: Scenario): string {
  const out: string[] = [];
  out.push("=== 合单拣货方案 ===");
  out.push(
    `拣货项 ${scenario.lines.length} 个, 车容量 ${scenario.capacity}, 拆单成本 ${scenario.splitCost}/订单/额外出趟`,
  );
  out.push("");
  for (const trip of plan.trips) {
    out.push(
      `车次 ${trip.index + 1}: 载荷 ${trip.load}/${scenario.capacity}, 行走距离 ${trip.distance}`,
    );
    out.push(`  路线: ${trip.route.join(" -> ")}`);
    out.push("  停靠拣货:");
    for (const stop of trip.stops) {
      const picks = stop.picks.map((p) => `${p.order}×${p.qty}`).join(", ");
      const stopLoad = stop.picks.reduce((acc, p) => acc + p.qty, 0);
      out.push(`    ${stop.location}: ${picks}  (共 ${stopLoad} 件)`);
    }
    out.push("");
  }
  const tripsPerOrder = new Map<string, number[]>();
  for (const trip of plan.trips) {
    const orders = new Set<string>();
    for (const stop of trip.stops) {
      for (const pick of stop.picks) orders.add(pick.order);
    }
    for (const order of orders) {
      if (!tripsPerOrder.has(order)) tripsPerOrder.set(order, []);
      tripsPerOrder.get(order)!.push(trip.index + 1);
    }
  }
  out.push("订单分趟情况:");
  for (const [order, trips] of [...tripsPerOrder.entries()].sort()) {
    const tag = trips.length > 1 ? `  (拆 ${trips.length} 趟)` : "";
    out.push(`  ${order}: 车次 ${trips.join(", ")}${tag}`);
  }
  out.push("");
  out.push(
    `总计: 行走 ${plan.walkingCost} + 拆单 ${plan.splitCost} = ${plan.totalCost}`,
  );
  return out.join("\n");
}

function main(): void {
  const arg = process.argv[2];
  const scenario: Scenario = arg
    ? (JSON.parse(readFileSync(arg, "utf8")) as Scenario)
    : demoScenario;
  const plan = planPicking(scenario);
  console.log(formatPlan(plan, scenario));
}

main();
