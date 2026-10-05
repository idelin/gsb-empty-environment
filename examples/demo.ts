import { scheduleCharging } from "../src/index.ts";
import type { ChargePlan } from "../src/index.ts";

function printPlan(title: string, plan: ChargePlan): void {
  console.log(`\n=== ${title} ===`);
  console.log(`可行: ${plan.feasible}  总费用: ${plan.totalCost}  交付: ${plan.totalDelivered}/${plan.totalDemand}`);
  for (const s of plan.schedules) {
    const sessions = s.sessions.map((x) => `[${x.startSlot},${x.endSlot})=${x.energy}`).join(" ");
    console.log(`  ${s.vehicleId}: 交付 ${s.deliveredEnergy} 费用 ${s.cost} 会话 ${sessions}`);
  }
  for (const u of plan.unmetDemand) {
    console.log(`  未满足 ${u.vehicleId}: 缺 ${u.missing}/${u.requested}`);
  }
  for (const d of plan.windowDeficits) {
    console.log(`  缺口 [${d.startSlot},${d.endSlot}) 需 ${d.requiredEnergy} 可供 ${d.availableEnergy} 缺 ${d.deficitEnergy} (${d.kind})`);
  }
}

// 场景 1：分时电价 + 站点功率约束 + 启动次数限制
const station = {
  slotCount: 8,
  powerLimitPerSlot: [20, 20, 20, 20, 10, 10, 20, 20],
  pricePerSlot: [3, 3, 1, 1, 5, 5, 2, 2],
};
const plan1 = scheduleCharging(
  [
    { id: "EV-1", arrivalSlot: 0, departureSlot: 8, demandEnergy: 40, maxPowerPerSlot: 15, maxStarts: 2 },
    { id: "EV-2", arrivalSlot: 2, departureSlot: 6, demandEnergy: 25, maxPowerPerSlot: 10 },
    { id: "EV-3", arrivalSlot: 4, departureSlot: 8, demandEnergy: 20, maxPowerPerSlot: 20, maxStarts: 1 },
  ],
  station,
);
printPlan("三车错峰调度", plan1);

// 场景 2：晚高峰容量不足，输出时间窗能量缺口
const plan2 = scheduleCharging(
  [
    { id: "EV-4", arrivalSlot: 4, departureSlot: 6, demandEnergy: 30, maxPowerPerSlot: 20 },
    { id: "EV-5", arrivalSlot: 4, departureSlot: 6, demandEnergy: 30, maxPowerPerSlot: 20 },
  ],
  station,
);
printPlan("高峰容量不足", plan2);
