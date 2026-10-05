import { Plan, Problem } from "./types";

const MINUTES_PER_DAY = 1440;

/** 绝对分钟格式化为 “D<第几天> HH:MM”，跨午夜自然进位，如 1500 -> D2 01:00 */
export function fmtTime(absMin: number): string {
  const day = Math.floor(absMin / MINUTES_PER_DAY) + 1;
  const rest = absMin % MINUTES_PER_DAY;
  const hh = Math.floor(rest / 60).toString().padStart(2, "0");
  const mm = (rest % 60).toString().padStart(2, "0");
  return `D${day} ${hh}:${mm}`;
}

/** 输出每次装卸、运输与批次衔接的完整方案 */
export function renderPlan(plan: Plan, problem: Problem): string {
  const { handling } = problem;
  const lines: string[] = [];

  if (!plan.feasible) {
    lines.push(`不可行: ${plan.reason ?? "未知原因"}`);
    if (plan.undeliverable.length > 0) {
      lines.push(`无法送达的批次: ${plan.undeliverable.join(", ")}`);
    }
    return lines.join("\n");
  }

  const lastArrival = Math.max(...plan.batches.map((b) => b.arrivedAt));
  lines.push(
    `总费用 ${plan.totalCost} | ${plan.moves.length} 个班次 | ${plan.batches.length} 批全部送达 | 最晚送达 ${fmtTime(lastArrival)}`,
  );
  lines.push("");
  lines.push("== 装卸与运输 ==");
  plan.moves.forEach((m, idx) => {
    lines.push(`#${idx + 1} 班次 ${m.trip.id}  ${m.trip.from} -> ${m.trip.to}`);
    lines.push(
      `  装载: ${fmtTime(m.loadBy)} 前于 ${m.trip.from} 装车 — ${m.batchIds.join(" + ")} (合装 ${m.totalSize}/${m.trip.capacity})`,
    );
    lines.push(`  运输: ${fmtTime(m.trip.depart)} 发 -> ${fmtTime(m.trip.arrive)} 到`);
    lines.push(`  卸载: ${fmtTime(m.availableAt)} 起于 ${m.trip.to} 可提`);
    lines.push(`  费用: ${m.cost}`);
  });

  const transferLines: string[] = [];
  for (const b of plan.batches) {
    for (let i = 0; i + 1 < b.legs.length; i++) {
      const prev = b.legs[i];
      const next = b.legs[i + 1];
      const availAt = prev.arrive + handling.unloadTime;
      const slack = next.depart - handling.loadTime - availAt;
      transferLines.push(
        `  ${b.batchId}: ${prev.to} ${fmtTime(availAt)} 可提 -> 衔接 ${next.tripId} (${fmtTime(next.depart)} 发)，缓冲 ${slack} 分钟`,
      );
    }
  }
  if (transferLines.length > 0) {
    lines.push("");
    lines.push("== 中转衔接 ==");
    lines.push(...transferLines);
  }

  lines.push("");
  lines.push("== 各批行程 ==");
  const originOf = new Map(problem.batches.map((b) => [b.id, b.origin] as const));
  for (const b of plan.batches) {
    const route = [originOf.get(b.batchId) as string, ...b.legs.map((l) => l.to)].join(" -> ");
    const via = b.legs.length > 0 ? `经 ${b.legs.map((l) => l.tripId).join(", ")}` : "无需运输";
    lines.push(`  ${b.batchId}: ${route} | ${via} | ${fmtTime(b.arrivedAt)} 送达`);
  }
  return lines.join("\n");
}
