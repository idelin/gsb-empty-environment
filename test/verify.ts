/**
 * 正确性校验：用暴力枚举作为参照，与 DP 求解器对比单门店最优成本。
 * 随机生成小规模实例（箱型 ≤4 种、需求 ≤60），两者结果必须一致。
 */
import { solve, solveStore } from "../src/solver";
import { BoxType, Store } from "../src/types";

function bruteForceStore(
  boxTypes: BoxType[],
  demand: number,
  penalty: number,
  transport: number
): number {
  const maxCap = Math.max.apply(null, boxTypes.map((b) => b.capacity));
  const upper = demand + maxCap - 1;
  const counts = new Array<number>(boxTypes.length).fill(0);
  let best = penalty * demand;

  function dfs(i: number, supplied: number, costSoFar: number): void {
    if (costSoFar >= best) return;
    if (i === boxTypes.length) {
      const shortage = demand - supplied > 0 ? demand - supplied : 0;
      const total = costSoFar + penalty * shortage;
      if (total < best) best = total;
      return;
    }
    const cap = boxTypes[i].capacity;
    const w = boxTypes[i].unitCost + transport;
    const maxCount = Math.floor((upper - supplied) / cap);
    for (let c = 0; c <= maxCount; c++) {
      dfs(i + 1, supplied + c * cap, costSoFar + c * w);
    }
  }
  dfs(0, 0, 0);
  return best;
}

// 简单可复现的随机数发生器
let seed = 123456789;
function rand(mod: number): number {
  seed = (seed * 1103515245 + 12345) % 2147483648;
  return seed % mod;
}

let failures = 0;
const TRIALS = 2000;
for (let t = 0; t < TRIALS; t++) {
  const nTypes = 1 + rand(4);
  const boxTypes: BoxType[] = [];
  const usedCaps = new Set<number>();
  for (let i = 0; i < nTypes; i++) {
    let cap = 1 + rand(9);
    while (usedCaps.has(cap)) cap = 1 + rand(9);
    usedCaps.add(cap);
    boxTypes.push({ id: `T${i}`, capacity: cap, unitCost: rand(21) });
  }
  const demand = rand(61);
  const penalty = rand(16);
  const transport = rand(6);
  const store: Store = { id: "S0", demand, shortagePenalty: penalty };

  const dp = solveStore(boxTypes, store, transport).totalCost;
  const bf = bruteForceStore(boxTypes, demand, penalty, transport);
  if (dp !== bf) {
    failures++;
    console.error(
      `不一致 #${t}: boxTypes=${JSON.stringify(boxTypes)} demand=${demand} penalty=${penalty} transport=${transport} dp=${dp} brute=${bf}`
    );
    if (failures > 5) break;
  }
}

if (failures > 0) {
  console.error(`失败: ${failures}/${TRIALS} 个实例不一致`);
  process.exit(1);
}
console.log(`通过: ${TRIALS} 个随机实例 DP 与暴力枚举结果一致`);

// 端到端冒烟测试：整体求解 + 结果一致性检查
const problem = {
  transportCostPerBox: 2,
  boxTypes: [
    { id: "S", capacity: 6, unitCost: 10 },
    { id: "M", capacity: 12, unitCost: 18 },
  ],
  stores: [
    { id: "A", demand: 45, shortagePenalty: 3 },
    { id: "B", demand: 23, shortagePenalty: 8 },
  ],
};
const sol = solve(problem);
const recomputed = sol.storePlans.reduce((a, p) => a + p.totalCost, 0);
if (sol.totalCost !== recomputed) {
  console.error("端到端测试失败: 总成本不等于各店成本之和");
  process.exit(1);
}
const supplied = sol.storePlans.reduce((a, p) => a + p.unitsSupplied, 0);
const capacityBought = sol.totalBoxes.reduce((a, b) => a + b.count * b.capacity, 0);
if (supplied !== capacityBought) {
  console.error("端到端测试失败: 各店配送量之和不等于采购总容量（存在跨店流转或拆箱）");
  process.exit(1);
}
console.log("通过: 端到端一致性检查（总成本、箱容量守衡）");
