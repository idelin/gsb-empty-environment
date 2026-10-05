import { existsSync, readFileSync } from "fs";
import { renderPlan } from "./format";
import { solve } from "./planner";
import { Problem } from "./types";

/**
 * 示例场景（时间均为绝对分钟，D1 00:00 起算）：
 * - B1/B2/B4 在 D1 18:00 备好，B3 在 D1 21:00 备好（赶不上 T1）；
 * - T1 到站 23:00，卸货 20 分钟 + 装货 30 分钟 = 23:50，赶不上 T3（23:40 发），
 *   即“到站时刻”不等于“可转运时刻”，T3 成为无法衔接的班次；
 * - T2 跨午夜（D1 23:30 发，D2 02:00 到）。
 */
export const sampleProblem: Problem = {
  handling: { loadTime: 30, unloadTime: 20 },
  batches: [
    { id: "B1", origin: "杭州", destination: "北京", readyTime: 1080, size: 4 },
    { id: "B2", origin: "杭州", destination: "北京", readyTime: 1080, size: 3 },
    { id: "B3", origin: "杭州", destination: "北京", readyTime: 1260, size: 4 },
    { id: "B4", origin: "杭州", destination: "上海", readyTime: 1080, size: 2 },
  ],
  trips: [
    { id: "T1", from: "杭州", to: "上海", depart: 1200, arrive: 1380, capacity: 10, fixedCost: 120, perUnitCost: 2 },
    { id: "T2", from: "杭州", to: "上海", depart: 1410, arrive: 1560, capacity: 6, fixedCost: 90, perUnitCost: 2 },
    { id: "T3", from: "上海", to: "北京", depart: 1420, arrive: 1700, capacity: 8, fixedCost: 200, perUnitCost: 3 },
    { id: "T4", from: "上海", to: "北京", depart: 1920, arrive: 2220, capacity: 12, fixedCost: 200, perUnitCost: 3 },
    { id: "T5", from: "杭州", to: "北京", depart: 1500, arrive: 2100, capacity: 5, fixedCost: 260, perUnitCost: 4 },
  ],
};

function main(): void {
  const arg = process.argv[2];
  let problem = sampleProblem;
  if (arg) {
    if (!existsSync(arg)) {
      console.error(`找不到文件: ${arg}`);
      process.exit(1);
    }
    problem = JSON.parse(readFileSync(arg, "utf8")) as Problem;
  }
  const plan = solve(problem);
  console.log(renderPlan(plan, problem));
}

main();
