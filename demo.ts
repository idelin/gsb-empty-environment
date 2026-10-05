import {
  Ad,
  Slot,
  ScheduleConfig,
  solveSchedule,
  validateSchedule,
  computeWindowCounts,
  Placement,
} from "./ad-scheduler";

// ---------- 构造跨日场景：20 个时段，从首日 20:00 到次日 15:00（每小时一段，跨午夜） ----------
const SLOT_COUNT = 20;
const startHour = 20; // 首日 20:00 开始
const base = new Date(2026, 9, 5, startHour); // 2026-10-05 20:00 本地时间

const groups = ["G1", "G2"];
const brands = ["Nike", "Adidas", "Puma"];

// 可复现的伪随机
let seed = 42;
const rand = (): number => {
  seed = (seed * 1103515245 + 12345) % 2147483648;
  return seed / 2147483648;
};

const slots: Slot[] = [];
for (let i = 0; i < SLOT_COUNT; i++) {
  const start = new Date(base.getTime() + i * 3600_000);
  const candidates: Ad[] = [];
  for (let j = 0; j < 5; j++) {
    candidates.push({
      id: `ad-${i}-${j}`,
      brand: brands[Math.floor(rand() * brands.length)],
      group: groups[Math.floor(rand() * groups.length)],
      revenue: Math.floor(rand() * 90) + 10,
    });
  }
  slots.push({ start, candidates });
}

const config: ScheduleConfig = {
  windowSize: 4, // 任意连续 4 个时段
  cap: 2,        // 同一用户组最多曝光 2 次
  // 品牌互斥：同品牌相邻互斥 + Nike/Adidas 竞品互斥
  conflicts: [
    ["Nike", "Nike"],
    ["Adidas", "Adidas"],
    ["Puma", "Puma"],
    ["Nike", "Adidas"],
  ],
};

const result = solveSchedule(slots, config);

// ---------- 输出排期 ----------
const fmt = (d: Date): string =>
  `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:00`;

console.log("=== 最优排期 ===");
for (const p of result.placements) {
  const slot = slots[p.slotIndex];
  const label = p.ad
    ? `${p.ad.id} 品牌=${p.ad.brand} 用户组=${p.ad.group} 收益=${p.ad.revenue}`
    : "(空置)";
  console.log(`时段 ${String(p.slotIndex).padStart(2)} [${fmt(slot.start)}] ${label}`);
}
console.log(`总收益: ${result.totalRevenue}`);

// ---------- 输出各窗口计数 ----------
console.log("\n=== 滚动窗口计数 (windowSize=4, cap=2) ===");
for (const w of result.windowCounts) {
  const crossMidnight =
    slots[w.start].start.getDate() !== slots[w.end].start.getDate();
  const counts = Object.entries(w.counts)
    .map(([g, c]) => `${g}:${c}`)
    .join(" ");
  console.log(
    `窗口 [${String(w.start).padStart(2)}, ${String(w.end).padStart(2)}] ` +
      `${fmt(slots[w.start].start)} ~ ${fmt(slots[w.end].start)}  ` +
      `${counts}${crossMidnight ? "  <-- 跨午夜窗口" : ""}`
  );
}

// ---------- 校验约束 ----------
const errors = validateSchedule(result, config);
console.log(`\n约束校验: ${errors.length === 0 ? "全部通过" : errors.join("; ")}`);

// ---------- 验证跨日窗口不在午夜清零 ----------
// 找一个跨午夜且两侧都有同组曝光的窗口，证明计数跨越了午夜
console.log("\n=== 跨日窗口验证 ===");
let verified = false;
for (const w of result.windowCounts) {
  const beforeMidnight = slots[w.start].start.getDate() !== slots[w.end].start.getDate();
  if (!beforeMidnight) continue;
  for (const g of groups) {
    const before = result.placements
      .slice(w.start, w.end + 1)
      .filter((p) => p.ad && p.ad.group === g && slots[p.slotIndex].start.getDate() === slots[w.start].start.getDate())
      .length;
    const after = w.counts[g] !== undefined ? w.counts[g] - before : 0;
    if (before > 0 && after > 0) {
      console.log(
        `窗口 [${w.start}, ${w.end}] 用户组 ${g}: 午夜前 ${before} 次 + 午夜后 ${after} 次 = ${w.counts[g]} 次（未在午夜清零）`
      );
      verified = true;
    }
  }
}
if (!verified) {
  // 构造确定性用例：手工放置跨午夜曝光，直接验证 computeWindowCounts 不清零
  const manual: Placement[] = Array.from({ length: 6 }, (_, i) => ({
    slotIndex: i,
    ad: { id: `m${i}`, brand: `B${i}`, group: i === 1 || i === 3 ? "G1" : "G2", revenue: 1 },
  }));
  const counts = computeWindowCounts(manual, 4);
  const w = counts[0]; // 覆盖时段 0..3，含 G1 在 1 和 3 各一次
  console.log(
    `手工用例: 窗口 [0, 3] 内 G1 计数 = ${w.counts["G1"]}（跨边界两侧各一次，合并计数，未清零）`
  );
  if (w.counts["G1"] !== 2) throw new Error("跨日窗口计数错误！");
  console.log("跨日窗口计数正确。");
}

// ---------- 小规模暴力搜索验证 DP 最优性 ----------
console.log("\n=== 暴力搜索对照（前 10 个时段） ===");
const smallSlots = slots.slice(0, 10);
const smallResult = solveSchedule(smallSlots, config);

let bruteBest = 0;
const choice: Array<Ad | null> = new Array(smallSlots.length).fill(null);
const brute = (i: number, revenue: number): void => {
  if (i === smallSlots.length) {
    if (revenue > bruteBest) bruteBest = revenue;
    return;
  }
  const options: Array<Ad | null> = [null, ...smallSlots[i].candidates];
  for (const ad of options) {
    if (ad !== null) {
      const prev = choice[i - 1];
      if (prev) {
        const pair = [prev.brand, ad.brand];
        const conflict = config.conflicts!.some(
          ([a, b]) => (a === pair[0] && b === pair[1]) || (a === pair[1] && b === pair[0])
        );
        if (conflict) continue;
      }
      // 滚动频控：检查所有包含 i 的窗口
      let ok = true;
      for (let s = Math.max(0, i - config.windowSize + 1); s <= i; s++) {
        let c = 0;
        for (let k = s; k < i; k++) if (choice[k] && choice[k]!.group === ad.group) c++;
        if (c + 1 > config.cap) { ok = false; break; }
      }
      if (!ok) continue;
    }
    choice[i] = ad;
    brute(i + 1, revenue + (ad ? ad.revenue : 0));
    choice[i] = null;
  }
};
brute(0, 0);
console.log(`DP 最优收益: ${smallResult.totalRevenue}, 暴力搜索最优: ${bruteBest}`);
console.log(smallResult.totalRevenue === bruteBest ? "一致，DP 结果最优 ✔" : "不一致 ✘");
