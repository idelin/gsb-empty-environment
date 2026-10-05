import { test } from "node:test";
import assert from "node:assert/strict";
import {
  solve,
  verifyPlan,
  countWindows,
  MAX_SLOTS,
  MAX_CANDIDATES_PER_SLOT,
} from "./scheduler.ts";
import type { Slot, FreqCap, Candidate, SolveOptions } from "./scheduler.ts";

/** 暴力枚举全部计划, 返回合法计划中的最大收益 (仅用于小规模对照) */
function bruteForce(slots: Slot[], options: SolveOptions): number {
  const caps = options.caps ?? [];
  const exclusive = new Map<string, Set<string>>();
  for (const [a, b] of options.exclusiveBrandPairs ?? []) {
    if (!exclusive.has(a)) exclusive.set(a, new Set());
    exclusive.get(a)!.add(b);
    if (!exclusive.has(b)) exclusive.set(b, new Set());
    exclusive.get(b)!.add(a);
  }
  const choice: Array<Candidate | null> = new Array(slots.length).fill(null);
  let best = 0;
  const rec = (i: number, revenue: number): void => {
    if (i === slots.length) {
      best = Math.max(best, revenue);
      return;
    }
    const tryPick = (cand: Candidate | null): void => {
      if (cand) {
        const prev = i > 0 ? choice[i - 1] : null;
        if (
          prev &&
          (prev.brand === cand.brand ||
            (exclusive.get(prev.brand)?.has(cand.brand) ?? false))
        ) {
          return;
        }
        for (const cap of caps) {
          if (!cand.groups.includes(cap.group)) continue;
          const start = Math.max(0, i - cap.windowSlots + 1);
          let count = 1;
          for (let j = start; j < i; j++) {
            const ad = choice[j];
            if (ad && ad.groups.includes(cap.group)) count++;
          }
          if (count > cap.max) return;
        }
      }
      choice[i] = cand;
      rec(i + 1, revenue + (cand?.revenue ?? 0));
      choice[i] = null;
    };
    tryPick(null);
    for (const cand of slots[i]!.candidates) tryPick(cand);
  };
  rec(0, 0);
  return best;
}

function randomSlots(n: number, seed: number): Slot[] {
  let s = seed;
  const rand = (): number => {
    s = (s * 1103515245 + 12345) & 0x7fffffff;
    return s / 0x7fffffff;
  };
  const brands = ["A", "B", "C"];
  const groupPool = ["g1", "g2"];
  const slots: Slot[] = [];
  for (let i = 0; i < n; i++) {
    const nCands = 1 + Math.floor(rand() * 3);
    const candidates: Candidate[] = [];
    for (let k = 0; k < nCands; k++) {
      candidates.push({
        adId: `a${i}-${k}`,
        brand: brands[Math.floor(rand() * brands.length)]!,
        groups: [groupPool[Math.floor(rand() * groupPool.length)]!],
        revenue: 1 + Math.floor(rand() * 20),
      });
    }
    slots.push({ id: `s${i}`, day: Math.floor(i / 4), candidates });
  }
  return slots;
}

test("DP 与暴力枚举结果一致 (随机小规模)", () => {
  for (let seed = 1; seed <= 30; seed++) {
    const slots = randomSlots(8, seed);
    const caps: FreqCap[] = [
      { group: "g1", windowSlots: 3, max: 1 + (seed % 2) },
      { group: "g2", windowSlots: 4, max: 2 },
    ];
    const options: SolveOptions = { caps, exclusiveBrandPairs: [["A", "B"]] };
    const result = solve(slots, options);
    assert.equal(result.totalRevenue, bruteForce(slots, options), `seed=${seed}`);
    assert.deepEqual(verifyPlan(slots, result.choices, options), [], `seed=${seed}`);
  }
});

test("滚动频控跨日不清零: 午夜不重置窗口", () => {
  // 4 个时段跨两天, 窗口长度 3, 上限 1。
  // 若在午夜清零, 时段 2 (新的一天) 可以投放; 滚动窗口下时段 0/1/2 同处一个窗口, 不可都投。
  const ad = (id: string, revenue: number): Candidate => ({
    adId: id,
    brand: id, // 不同品牌, 排除互斥干扰
    groups: ["g"],
    revenue,
  });
  const slots: Slot[] = [
    { id: "D0-22", day: 0, candidates: [ad("x", 5)] },
    { id: "D0-23", day: 0, candidates: [ad("y", 5)] },
    { id: "D1-00", day: 1, candidates: [ad("z", 5)] },
    { id: "D1-01", day: 1, candidates: [ad("w", 5)] },
  ];
  const caps: FreqCap[] = [{ group: "g", windowSlots: 3, max: 1 }];
  const result = solve(slots, { caps });

  // 最优: 时段 0 与 3 (间隔 3 个时段, 不在同一窗口) -> 收益 10
  assert.equal(result.totalRevenue, 10);
  assert.deepEqual(
    result.choices.map((c) => c?.adId ?? null),
    ["x", null, null, "w"],
  );

  // 跨日窗口 [0..2] 与 [1..3] 的计数都必须 <= 1
  const windows = countWindows(slots, result.choices, caps);
  const cross = windows.filter((w) => w.spansMidnight);
  assert.ok(cross.length > 0, "应存在跨日窗口");
  for (const w of cross) assert.ok(w.count <= 1, `跨日窗口 [${w.startSlot}..${w.endSlot}] 超cap`);
  // 关键断言: 若午夜清零, 时段 2 的 z 会被认为可投 (窗口内同日计数为 0),
  // 但滚动窗口 [0..2] 已含 x, 因此 z 不可投 —— 最优解中 z 必须为空放。
  assert.equal(result.choices[2], null);
});

test("相邻品牌互斥: 同品牌与互斥品牌对", () => {
  const slots: Slot[] = [0, 1].map((i) => ({
    id: `s${i}`,
    day: 0,
    candidates: [
      { adId: `cola${i}`, brand: "Cola", groups: [], revenue: 100 },
      { adId: `pepsi${i}`, brand: "Pepsi", groups: [], revenue: 90 },
      { adId: `nike${i}`, brand: "Nike", groups: [], revenue: 10 },
    ],
  }));
  const result = solve(slots, { exclusiveBrandPairs: [["Cola", "Pepsi"]] });
  // 不能 Cola->Cola (同品牌), 也不能 Cola->Pepsi (互斥对), 最优为 Cola + Nike = 110
  assert.equal(result.totalRevenue, 110);
  assert.deepEqual(
    result.choices.map((c) => c?.brand),
    ["Cola", "Nike"],
  );
});

test("输入校验: 时段数与候选数上限", () => {
  const slot: Slot = { id: "s", day: 0, candidates: [] };
  assert.throws(() => solve(new Array(MAX_SLOTS + 1).fill(slot)));
  const tooMany: Slot = {
    id: "s",
    day: 0,
    candidates: Array.from({ length: MAX_CANDIDATES_PER_SLOT + 1 }, (_, k) => ({
      adId: `a${k}`,
      brand: "B",
      groups: [],
      revenue: 1,
    })),
  };
  assert.throws(() => solve([tooMany]));
});

test("20 时段上限规模可求解", () => {
  const slots = randomSlots(MAX_SLOTS, 99);
  const result = solve(slots, {
    caps: [
      { group: "g1", windowSlots: 5, max: 2 },
      { group: "g2", windowSlots: 7, max: 3 },
    ],
  });
  assert.ok(result.totalRevenue > 0);
  assert.equal(result.choices.length, MAX_SLOTS);
});
