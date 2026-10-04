import { readFileSync } from "fs";
import { planReplenishment } from "./solver";
import { Plan, PlanInput } from "./types";

function fmt(n: number): string {
  return Number.isInteger(n) ? String(n) : n.toFixed(2);
}

export function renderPlan(plan: Plan): string {
  const lines: string[] = [];
  lines.push("===== 门店补货计划（混合整箱配送） =====");
  for (const s of plan.stores) {
    lines.push(`\n门店 ${s.storeId}: 需求 ${s.demand} 件`);
    const boxDesc =
      Object.entries(s.boxes)
        .map(([id, n]) => `${id} x ${n}`)
        .join(", ") || "(不配送)";
    lines.push(`  箱型分配: ${boxDesc}  (共 ${s.totalBoxes} 箱)`);
    lines.push(
      `  送达 ${s.delivered} 件 | 欠供 ${s.shortage} 件 | 剩余库存 ${s.leftover} 件`
    );
    lines.push(
      `  成本: 采购 ${fmt(s.purchaseCost)} + 运输 ${fmt(s.transportCost)}` +
        ` + 欠供罚金 ${fmt(s.shortagePenalty)} = ${fmt(s.totalCost)}`
    );
  }
  lines.push("\n===== 汇总 =====");
  lines.push(
    `总送达 ${plan.totalDelivered} 件 | 总欠供 ${plan.totalShortage} 件 | 总剩余库存 ${plan.totalLeftover} 件`
  );
  lines.push(
    `总成本: 采购 ${fmt(plan.purchaseCost)} + 运输 ${fmt(plan.transportCost)}` +
      ` + 欠供罚金 ${fmt(plan.shortagePenalty)} = ${fmt(plan.totalCost)}`
  );
  return lines.join("\n");
}

function main(): void {
  const args = process.argv.slice(2);
  const asJson = args.includes("--json");
  const file = args.find((a) => a !== "--json");

  const raw = file
    ? readFileSync(file, "utf-8")
    : readFileSync(0, "utf-8"); // stdin
  const input = JSON.parse(raw) as PlanInput;
  const plan = planReplenishment(input);

  if (asJson) {
    process.stdout.write(JSON.stringify(plan, null, 2) + "\n");
  } else {
    process.stdout.write(renderPlan(plan) + "\n");
  }
}

main();
