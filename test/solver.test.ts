import { strict as assert } from "assert";
import { planReplenishment, planStore, validateInput } from "../src/solver";
import { BoxType, Store } from "../src/types";

const boxes: BoxType[] = [
  { id: "A", capacity: 6, purchaseCost: 30, transportCost: 0 },
  { id: "B", capacity: 12, purchaseCost: 55, transportCost: 0 },
];

function store(demand: number, penalty: number): Store {
  return { id: "S1", demand, shortagePenaltyPerUnit: penalty };
}

// 1. 精确匹配：需求 24 = 2 箱 B
{
  const p = planStore(boxes, store(24, 5));
  assert.deepEqual(p.boxes, { B: 2 });
  assert.equal(p.delivered, 24);
  assert.equal(p.shortage, 0);
  assert.equal(p.leftover, 0);
  assert.equal(p.totalCost, 110);
}

// 2. 高罚金 -> 宁可超送整箱也不欠供：需求 25，罚金 100
{
  const p = planStore(boxes, store(25, 100));
  assert.equal(p.shortage, 0);
  assert.ok(p.delivered >= 25);
  assert.equal(p.leftover, p.delivered - 25);
}

// 3. 低罚金 -> 允许欠供：需求 25，罚金 5/件（欠 1 件罚 5，比加发一箱便宜）
{
  const p = planStore(boxes, store(25, 5));
  assert.equal(p.delivered, 24);
  assert.equal(p.shortage, 1);
  assert.equal(p.shortagePenalty, 5);
  assert.equal(p.totalCost, 115);
}

// 4. 零需求 -> 不配送
{
  const p = planStore(boxes, store(0, 5));
  assert.equal(p.totalBoxes, 0);
  assert.equal(p.totalCost, 0);
}

// 5. 罚金为 0 -> 完全不配送最优
{
  const p = planStore(boxes, store(50, 0));
  assert.equal(p.totalBoxes, 0);
  assert.equal(p.shortage, 50);
  assert.equal(p.totalCost, 0);
}

// 6. 按门店覆盖运输成本
{
  const remote: BoxType[] = [
    { id: "A", capacity: 10, purchaseCost: 10, transportCost: 1, transportCostByStore: { FAR: 100 } },
    { id: "B", capacity: 20, purchaseCost: 10, transportCost: 1 },
  ];
  const near = planStore(remote, { id: "NEAR", demand: 10, shortagePenaltyPerUnit: 50 });
  assert.deepEqual(near.boxes, { A: 1 });
  const far = planStore(remote, { id: "FAR", demand: 10, shortagePenaltyPerUnit: 50 });
  assert.deepEqual(far.boxes, { B: 1 }); // A 运费暴涨，改发 B 整箱
  assert.equal(far.leftover, 10);
}

// 7. 全局汇总 + 剩余库存不跨店流转（各店 leftover 独立累计）
{
  const plan = planReplenishment({
    boxTypes: boxes,
    stores: [
      { id: "S1", demand: 13, shortagePenaltyPerUnit: 100 },
      { id: "S2", demand: 5, shortagePenaltyPerUnit: 100 },
    ],
  });
  assert.equal(plan.stores.length, 2);
  const leftoverSum = plan.stores.reduce((a, p) => a + p.leftover, 0);
  assert.equal(plan.totalLeftover, leftoverSum);
  assert.equal(
    plan.totalCost,
    plan.purchaseCost + plan.transportCost + plan.shortagePenalty
  );
  // S1: 13 -> B+A=18 件(余5) 或 2B=24(余11)... 最优 B+A=18 成本 85
  assert.equal(plan.stores[0].delivered, 18);
  // S2: 5 -> A=6 件(余1) 成本 30
  assert.equal(plan.stores[1].delivered, 6);
}

// 8. 规模约束：>8 箱型 / >5 门店报错
{
  const manyBoxes = Array.from({ length: 9 }, (_, i) => ({
    id: `B${i}`, capacity: 1, purchaseCost: 1, transportCost: 0,
  }));
  assert.throws(() => validateInput({ boxTypes: manyBoxes, stores: [store(1, 1)] }), /最多 8/);
  const manyStores = Array.from({ length: 6 }, (_, i) => ({
    id: `S${i}`, demand: 1, shortagePenaltyPerUnit: 1,
  }));
  assert.throws(() => validateInput({ boxTypes: boxes, stores: manyStores }), /最多 5/);
}

// 9. maxShortage 限制欠供上限
{
  const p = planStore(boxes, { id: "S1", demand: 25, shortagePenaltyPerUnit: 1, maxShortage: 2 });
  assert.ok(p.shortage <= 2);
  assert.equal(p.delivered, 24); // 欠 1 件满足上限，且比送 30 件便宜
  // maxShortage=0 时必须足量（可靠超送整箱满足）
  const full = planStore(boxes, { id: "S1", demand: 25, shortagePenaltyPerUnit: 1, maxShortage: 0 });
  assert.equal(full.shortage, 0);
  assert.ok(full.delivered >= 25);
}

// 10. 暴力对拍：小规模随机用例与穷举结果一致
{
  let seed = 42;
  const rand = (n: number) => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed % n;
  };
  for (let iter = 0; iter < 200; iter++) {
    const nTypes = 1 + rand(3);
    const bts: BoxType[] = Array.from({ length: nTypes }, (_, i) => ({
      id: `T${i}`,
      capacity: 1 + rand(12),
      purchaseCost: rand(50),
      transportCost: rand(10),
    }));
    const st: Store = { id: "X", demand: rand(40), shortagePenaltyPerUnit: rand(20) };

    // 穷举所有箱数组合（每种 0..上界）
    const maxCap = Math.max(...bts.map((b) => b.capacity));
    const bound = Math.ceil((st.demand + maxCap) / Math.min(...bts.map((b) => b.capacity))) + 1;
    let brute = Infinity;
    const counts = new Array(nTypes).fill(0);
    const search = (t: number) => {
      if (t === nTypes) {
        const delivered = counts.reduce((a, c, i) => a + c * bts[i].capacity, 0);
        const cost =
          counts.reduce((a, c, i) => a + c * (bts[i].purchaseCost + bts[i].transportCost), 0) +
          st.shortagePenaltyPerUnit * Math.max(0, st.demand - delivered);
        brute = Math.min(brute, cost);
        return;
      }
      for (let n = 0; n <= bound; n++) {
        counts[t] = n;
        search(t + 1);
      }
    };
    search(0);

    const p = planStore(bts, st);
    assert.equal(p.totalCost, brute, `iter=${iter} 期望 ${brute} 实际 ${p.totalCost}`);
  }
}

console.log("全部测试通过 ✔");
