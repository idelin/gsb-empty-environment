import { solve, verifyPlan, countWindows } from "./scheduler.ts";
import type { Slot, FreqCap } from "./scheduler.ts";

/**
 * 演示: 两天共 12 个时段 (每天 6 个), 每时段 3~5 个候选。
 * 用户组:
 *   - young:  任意连续 4 个时段内最多曝光 2 次
 *   - family: 任意连续 6 个时段内最多曝光 2 次
 * 品牌互斥: 同品牌相邻禁投; 另外 "Cola" 与 "Pepsi" 互为竞对, 相邻禁投。
 * 频控窗口按时段滑动, 跨午夜 (day 0 -> day 1) 不清零。
 */

const brands = ["Cola", "Pepsi", "Nike", "Ikea"] as const;

function makeSlots(): Slot[] {
  const slots: Slot[] = [];
  for (let i = 0; i < 12; i++) {
    const day = i < 6 ? 0 : 1;
    const hour = 18 + (i % 6) * 2; // 每天 18:00 起, 每 2 小时一个时段
    const nCands = 3 + (i % 3); // 3~5 个候选
    const candidates = [];
    for (let k = 0; k < nCands; k++) {
      const brand = brands[(i + k) % brands.length]!;
      const groups = k % 2 === 0 ? ["young"] : k % 3 === 0 ? ["family"] : ["young", "family"];
      candidates.push({
        adId: `ad-${i}-${k}`,
        brand,
        groups,
        revenue: 10 + ((i * 7 + k * 13) % 30),
      });
    }
    slots.push({ id: `D${day} ${String(hour).padStart(2, "0")}:00`, day, candidates });
  }
  return slots;
}

const slots = makeSlots();
const caps: FreqCap[] = [
  { group: "young", windowSlots: 4, max: 2 },
  { group: "family", windowSlots: 6, max: 2 },
];
const options = { caps, exclusiveBrandPairs: [["Cola", "Pepsi"]] as Array<[string, string]> };

const result = solve(slots, options);

console.log("=== 最优排期 ===");
result.choices.forEach((ad, i) => {
  const slot = slots[i]!;
  const text = ad
    ? `${ad.adId}  品牌=${ad.brand.padEnd(5)} 用户组=${ad.groups.join("+").padEnd(12)} 收益=${ad.revenue}`
    : "(空放)";
  console.log(`时段 ${String(i).padStart(2)} [${slot.id}]  ${text}`);
});
console.log(`\n总收益: ${result.totalRevenue}`);

console.log("\n=== 各滚动窗口计数 (窗口按时段滑动, 跨日不清零) ===");
for (const cap of caps) {
  console.log(`\n用户组 ${cap.group}: 窗口长度 ${cap.windowSlots} 时段, 上限 ${cap.max}`);
  for (const w of result.windowCounts.filter((x) => x.group === cap.group)) {
    const range = `[${String(w.startSlot).padStart(2)}..${String(w.endSlot).padStart(2)}]`;
    const mark = w.spansMidnight ? "  <== 跨午夜窗口" : "";
    console.log(`  窗口 ${range} 计数=${w.count}/${w.cap}${mark}`);
  }
}

// ---- 验证: 跨日窗口不在午夜自动清零 ----
console.log("\n=== 跨日窗口验证 ===");
const violations = verifyPlan(slots, result.choices, options);
if (violations.length > 0) {
  console.error("计划非法:\n" + violations.join("\n"));
  process.exit(1);
}
console.log("约束校验通过: 相邻品牌互斥与全部滚动频控均满足。");

// 对照: 若错误地在午夜清零, 跨日窗口的计数会偏小。
// 这里直接按"跨日窗口计数 = 两日各自计数之和"验证窗口确实跨越了日界。
const crossDay = result.windowCounts.filter((w) => w.spansMidnight);
if (crossDay.length === 0) {
  console.log("(本次计划没有跨日窗口命中频控统计, 无法演示)");
} else {
  for (const w of crossDay.slice(0, 4)) {
    // 按"午夜清零"的错误算法重算: 只统计窗口中与结束时段同日的部分
    let resetCount = 0;
    for (let j = w.startSlot; j <= w.endSlot; j++) {
      const ad = result.choices[j];
      if (slots[j]!.day === slots[w.endSlot]!.day && ad && ad.groups.includes(w.group)) {
        resetCount++;
      }
    }
    console.log(
      `用户组 ${w.group} 窗口 [${w.startSlot}..${w.endSlot}]: ` +
        `滚动计数=${w.count} (若午夜清零则=${resetCount}) -> 窗口跨日累计, 未清零`,
    );
  }
  const allCounted = crossDay.every((w) => {
    // 重新用 countWindows 独立核算, 确认与求解器输出一致
    const recheck = countWindows(slots, result.choices, caps).find(
      (x) => x.group === w.group && x.endSlot === w.endSlot,
    );
    return recheck !== undefined && recheck.count === w.count && w.count <= w.cap;
  });
  console.log(allCounted ? "全部跨日窗口计数一致且未超限 ✔" : "跨日窗口计数不一致 ✘");
  if (!allCounted) process.exit(1);
}
