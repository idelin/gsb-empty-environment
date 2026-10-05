import { sampleProblem } from "../src/demo";
import { fmtTime } from "../src/format";
import { solve } from "../src/planner";
import { Problem } from "../src/types";

let failures = 0;

function check(cond: boolean, msg: string): void {
  if (!cond) {
    failures++;
    console.error(`FAIL: ${msg}`);
  } else {
    console.log(`ok: ${msg}`);
  }
}

function eq(actual: unknown, expected: unknown, msg: string): void {
  check(actual === expected, `${msg} (期望 ${expected}，实际 ${actual})`);
}

// 1. 示例场景：合车、跨午夜、装卸时间阻断“站到即转”
{
  const plan = solve(sampleProblem);
  check(plan.feasible, "示例场景可行");
  eq(plan.totalCost, 469, "示例场景最小总费用");
  eq(plan.moves.length, 3, "示例场景使用 3 个班次");
  const tripIds = plan.moves.map((m) => m.trip.id).sort();
  eq(tripIds.join(","), "T1,T2,T4", "T3 因装卸时间不足无法衔接，T5 直达不经济");
  for (const m of plan.moves) {
    check(m.totalSize <= m.trip.capacity, `班次 ${m.trip.id} 未超载 (${m.totalSize}/${m.trip.capacity})`);
  }
  const arrived = new Map(plan.batches.map((b) => [b.batchId, b.arrivedAt] as const));
  check(arrived.get("B4") === 1400 || arrived.get("B4") === 1580, "B4 经 T1 或 T2 送达上海（同价最优）");
  eq(arrived.get("B1"), 2240, "B1 于 D2 13:20 送达北京");
  eq(arrived.get("B3"), 2240, "B3 于 D2 13:20 送达北京");
}

// 2. 转运缓冲：arrive + unload + load <= depart 才允许衔接
{
  const base: Problem = {
    handling: { loadTime: 30, unloadTime: 20 },
    batches: [{ id: "X", origin: "A", destination: "C", readyTime: 0, size: 1 }],
    trips: [
      { id: "L1", from: "A", to: "B", depart: 900, arrive: 1000, capacity: 5, fixedCost: 10 },
      { id: "L2", from: "B", to: "C", depart: 1049, arrive: 1200, capacity: 5, fixedCost: 10 },
    ],
  };
  const tight = solve(base);
  check(!tight.feasible, "缓冲 49 分钟 < 50 分钟装卸，衔接失败");
  eq(tight.undeliverable.join(","), "X", "不可行时报告问题批次");

  const enough: Problem = {
    ...base,
    trips: [base.trips[0], { ...base.trips[1], depart: 1050 }],
  };
  const ok = solve(enough);
  check(ok.feasible, "缓冲 50 分钟 = 装卸时间，衔接成功");
  eq(ok.totalCost, 20, "两段运输费用合计");

  const immediate: Problem = {
    ...base,
    trips: [base.trips[0], { ...base.trips[1], depart: 1000 }],
  };
  check(!solve(immediate).feasible, "到站时刻不等于可转运时刻（同刻发车不可衔接）");
}

// 3. 容量：两批合车超载则必须分车
{
  const problem: Problem = {
    handling: { loadTime: 0, unloadTime: 0 },
    batches: [
      { id: "P", origin: "A", destination: "B", readyTime: 0, size: 6 },
      { id: "Q", origin: "A", destination: "B", readyTime: 0, size: 6 },
    ],
    trips: [
      { id: "V1", from: "A", to: "B", depart: 100, arrive: 200, capacity: 10, fixedCost: 50 },
      { id: "V2", from: "A", to: "B", depart: 300, arrive: 400, capacity: 10, fixedCost: 50 },
    ],
  };
  const plan = solve(problem);
  check(plan.feasible, "超载时拆分为两班可行");
  eq(plan.totalCost, 100, "两个班次各收一次固定费");
  eq(plan.moves.length, 2, "共 2 个班次");
}

// 4. 班次是单次事件：同一班次不能重复使用
{
  const problem: Problem = {
    handling: { loadTime: 0, unloadTime: 0 },
    batches: [
      { id: "P", origin: "A", destination: "B", readyTime: 0, size: 6 },
      { id: "Q", origin: "A", destination: "B", readyTime: 0, size: 6 },
    ],
    trips: [{ id: "V1", from: "A", to: "B", depart: 100, arrive: 200, capacity: 10, fixedCost: 50 }],
  };
  const plan = solve(problem);
  check(!plan.feasible, "唯一班次容量不足且不可重复使用 -> 不可行");
  eq(plan.undeliverable.length, 0, "单批均可达，是合车载重限制导致不可行");
}

// 5. 合车省固定费
{
  const problem: Problem = {
    handling: { loadTime: 0, unloadTime: 0 },
    batches: [
      { id: "P", origin: "A", destination: "B", readyTime: 0, size: 3 },
      { id: "Q", origin: "A", destination: "B", readyTime: 0, size: 4 },
    ],
    trips: [
      { id: "V1", from: "A", to: "B", depart: 100, arrive: 200, capacity: 10, fixedCost: 100 },
      { id: "V2", from: "A", to: "B", depart: 300, arrive: 400, capacity: 10, fixedCost: 100 },
    ],
  };
  const plan = solve(problem);
  eq(plan.totalCost, 100, "合装一车只收一次固定费");
  eq(plan.moves.length, 1, "仅用一个班次");
  eq(plan.moves[0].batchIds.length, 2, "两批同车");
}

// 6. readyTime 与装货时间共同决定能否赶上班次
{
  const problem: Problem = {
    handling: { loadTime: 30, unloadTime: 0 },
    batches: [{ id: "X", origin: "A", destination: "B", readyTime: 200, size: 1 }],
    trips: [
      { id: "E1", from: "A", to: "B", depart: 220, arrive: 300, capacity: 5, fixedCost: 10 },
      { id: "E2", from: "A", to: "B", depart: 230, arrive: 330, capacity: 5, fixedCost: 10 },
    ],
  };
  const plan = solve(problem);
  check(plan.feasible, "备好 + 装货 = 230，赶上第二班");
  eq(plan.moves[0].trip.id, "E2", "第一班 220 发车赶不上");
}

// 7. 跨午夜绝对分钟格式化
{
  eq(fmtTime(0), "D1 00:00", "fmtTime(0)");
  eq(fmtTime(1439), "D1 23:59", "fmtTime(1439)");
  eq(fmtTime(1440), "D2 00:00", "fmtTime(1440)");
  eq(fmtTime(1560), "D2 02:00", "fmtTime(1560)");
}

// 8. 输入校验：超过十批抛错
{
  const batches = [];
  for (let i = 0; i < 11; i++) {
    batches.push({ id: `B${i}`, origin: "A", destination: "B", readyTime: 0, size: 1 });
  }
  let threw = false;
  try {
    solve({ handling: { loadTime: 0, unloadTime: 0 }, batches, trips: [] });
  } catch {
    threw = true;
  }
  check(threw, "超过 10 批抛出错误");
}

if (failures > 0) {
  console.error(`\n${failures} 个断言失败`);
  process.exit(1);
}
console.log("\n全部测试通过");
