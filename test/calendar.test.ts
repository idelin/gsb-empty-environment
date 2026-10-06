import { describe, expect, test } from "bun:test";
import {
  addWorkTime,
  applyPauseDays,
  effectiveCalendar,
  pauseDayRange,
  unionIntervals,
  type Interval,
} from "../src/calendar";

const MIN = 60_000;
const HOUR = 3_600_000;
const DAY = 86_400_000;
const ORIGIN = Date.UTC(2026, 0, 1); // 2026-01-01T00:00:00Z

function at(dayOffset: number, hour = 0, minute = 0): number {
  return ORIGIN + dayOffset * DAY + hour * HOUR + minute * MIN;
}

function iso(ms: number): string {
  return new Date(ms).toISOString();
}

describe("unionIntervals", () => {
  test("合并重叠、包含与相邻区间，忽略空区间并排序", () => {
    const merged = unionIntervals([
      { start: at(2, 9), end: at(2, 17) },
      { start: at(0, 9), end: at(0, 12) },
      { start: at(0, 11), end: at(0, 13) }, // 与上一段重叠
      { start: at(0, 13), end: at(0, 17) }, // 与并集相邻
      { start: at(0, 10), end: at(0, 10) }, // 空区间
      { start: at(4, 9), end: at(4, 12) },
    ]);
    expect(merged).toEqual([
      { start: at(0, 9), end: at(0, 17) },
      { start: at(2, 9), end: at(2, 17) },
      { start: at(4, 9), end: at(4, 12) },
    ]);
  });
});

describe("pauseDayRange", () => {
  test("解析为 UTC 全天半开区间", () => {
    expect(pauseDayRange("2026-01-02")).toEqual({ start: at(1), end: at(2) });
  });

  test("拒绝非法日期", () => {
    expect(() => pauseDayRange("2026-13-01")).toThrow();
    expect(() => pauseDayRange("2026-02-30")).toThrow();
    expect(() => pauseDayRange("not-a-date")).toThrow();
  });
});

describe("applyPauseDays", () => {
  test("暂停日删除当天全部开放时间，跨午夜的区间被切分", () => {
    const result = applyPauseDays(
      [{ start: at(1, 20), end: at(3, 4) }], // 跨两个午夜的开放区间
      ["2026-01-03"], // ORIGIN + 2 天
    );
    expect(result).toEqual([
      { start: at(1, 20), end: at(2) },
      { start: at(3), end: at(3, 4) },
    ]);
  });
});

describe("addWorkTime 确定性用例", () => {
  const workday = (d: number): Interval => ({ start: at(d, 9), end: at(d, 17) });

  test("单区间内累加", () => {
    const r = addWorkTime(at(0, 10), 2 * HOUR, [workday(0)]);
    expect(r.end).toBe(at(0, 12));
    expect(r.segments).toEqual([{ start: at(0, 10), end: at(0, 12) }]);
  });

  test("到达右端恰好完成合法，不需要后续区间", () => {
    const r = addWorkTime(at(0, 16), 1 * HOUR, [workday(0)]);
    expect(r.end).toBe(at(0, 17));
    expect(r.segments).toEqual([{ start: at(0, 16), end: at(0, 17) }]);
  });

  test("负向倒推恰好到达左端合法", () => {
    const r = addWorkTime(at(0, 17), -8 * HOUR, [workday(0)]);
    expect(r.end).toBe(at(0, 9));
    expect(r.segments).toEqual([{ start: at(0, 9), end: at(0, 17) }]);
  });

  test("跨天累加，证据覆盖两段开放时间", () => {
    const r = addWorkTime(at(0, 16), 2 * HOUR, [workday(0), workday(1)]);
    expect(r.end).toBe(at(1, 10));
    expect(r.segments).toEqual([
      { start: at(0, 16), end: at(0, 17) },
      { start: at(1, 9), end: at(1, 10) },
    ]);
  });

  test("起点落在非开放时间，从下一开放段开始计", () => {
    const r = addWorkTime(at(0, 20), 1 * HOUR, [workday(0), workday(1)]);
    expect(r.end).toBe(at(1, 10));
    expect(r.segments).toEqual([{ start: at(1, 9), end: at(1, 10) }]);
  });

  test("重叠区间先并集，不会重复计时", () => {
    const r = addWorkTime(at(0, 9), 8 * HOUR, [
      { start: at(0, 9), end: at(0, 14) },
      { start: at(0, 12), end: at(0, 17) },
    ]);
    expect(r.end).toBe(at(0, 17));
    expect(r.segments).toEqual([{ start: at(0, 9), end: at(0, 17) }]);
  });

  test("暂停日覆盖当天全部开放时间", () => {
    const r = addWorkTime(at(0, 16), 2 * HOUR, [workday(0), workday(1), workday(2)], [
      "2026-01-02", // day offset 1 全休
    ]);
    expect(r.end).toBe(at(2, 10));
    expect(r.segments).toEqual([
      { start: at(0, 16), end: at(0, 17) },
      { start: at(2, 9), end: at(2, 10) },
    ]);
  });

  test("负向倒推跨天与暂停日", () => {
    const r = addWorkTime(at(2, 10), -2 * HOUR, [workday(0), workday(1), workday(2)], [
      "2026-01-02",
    ]);
    expect(r.end).toBe(at(0, 16));
    expect(r.segments).toEqual([
      { start: at(0, 16), end: at(0, 17) },
      { start: at(2, 9), end: at(2, 10) },
    ]);
  });

  test("全部暂停日时日历不足，抛 RangeError", () => {
    expect(() =>
      addWorkTime(at(0, 9), 1 * HOUR, [workday(0)], ["2026-01-01"]),
    ).toThrow(RangeError);
  });

  test("超出日历末端抛 RangeError", () => {
    expect(() => addWorkTime(at(0, 16), 2 * HOUR, [workday(0)])).toThrow(RangeError);
    expect(() => addWorkTime(at(0, 10), -2 * HOUR, [workday(0)])).toThrow(RangeError);
  });

  test("零时长原样返回，无证据", () => {
    const r = addWorkTime(at(0, 12), 0, [workday(0)]);
    expect(r).toEqual({ end: at(0, 12), segments: [] });
  });
});

/**
 * 逐分钟有限日历：把有限时间窗切成分钟格，逐分钟模拟累加，
 * 作为区间实现的独立参照（oracle）。
 */
function minuteCalendar(open: readonly Interval[], pauseDays: readonly string[], days: number) {
  const total = days * 24 * 60;
  const grid = new Array<boolean>(total).fill(false);
  for (const iv of effectiveCalendar(open, pauseDays)) {
    const from = Math.max(0, (iv.start - ORIGIN) / MIN);
    const to = Math.min(total, (iv.end - ORIGIN) / MIN);
    for (let i = from; i < to; i++) grid[i] = true;
  }
  return grid;
}

/** 逐分钟模拟；日历不足返回 null。要求 start 与 duration 均按分钟对齐。 */
function bruteAdd(grid: readonly boolean[], start: number, durationMs: number): number | null {
  let t = start;
  let remaining = durationMs / MIN;
  while (remaining > 0) {
    const i = (t - ORIGIN) / MIN;
    if (i < 0 || i >= grid.length) return null;
    if (grid[i]) remaining -= 1;
    t += MIN;
  }
  while (remaining < 0) {
    const i = (t - ORIGIN) / MIN - 1;
    if (i < 0 || i >= grid.length) return null;
    if (grid[i]) remaining += 1;
    t -= MIN;
  }
  return t;
}

/** 确定性伪随机数（mulberry32）。 */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

describe("逐分钟有限日历核对", () => {
  const DAYS = 14;
  const rand = rng(20261007);

  function randomCase() {
    // 随机开放区间：分钟对齐、可重叠、可乱序
    const count = 2 + Math.floor(rand() * 6);
    const horizon = DAYS * 24 * 60;
    const open: Interval[] = [];
    for (let k = 0; k < count; k++) {
      const s = Math.floor(rand() * horizon);
      const len = 1 + Math.floor(rand() * 60 * 20); // 最长 20 小时，易跨天
      // 裁剪到有限日历窗口内，与逐分钟参照的边界一致
      open.push({ start: ORIGIN + s * MIN, end: ORIGIN + Math.min(horizon, s + len) * MIN });
    }
    // 随机暂停日（含“全休”场景：所有天都暂停）
    const pauseDays: string[] = [];
    for (let d = 0; d < DAYS; d++) {
      if (rand() < 0.2) pauseDays.push(new Date(at(d)).toISOString().slice(0, 10));
    }
    if (rand() < 0.05) {
      pauseDays.length = 0;
      for (let d = 0; d < DAYS; d++) pauseDays.push(new Date(at(d)).toISOString().slice(0, 10));
    }
    const start = ORIGIN + Math.floor(rand() * DAYS * 24 * 60) * MIN;
    const sign = rand() < 0.4 ? -1 : 1; // 覆盖负向倒推
    const duration = sign * Math.floor(rand() * 18 * 60) * MIN;
    return { open, pauseDays, start, duration };
  }

  test("随机用例与逐分钟模拟一致（跨天/全休/负向）", () => {
    let forward = 0;
    let backward = 0;
    let insufficient = 0;
    for (let iter = 0; iter < 2000; iter++) {
      const { open, pauseDays, start, duration } = randomCase();
      const grid = minuteCalendar(open, pauseDays, DAYS);
      const expected = bruteAdd(grid, start, duration);

      let actual: number | null = null;
      let segments: Interval[] = [];
      try {
        const r = addWorkTime(start, duration, open, pauseDays);
        actual = r.end;
        segments = r.segments;
      } catch (e) {
        expect(e).toBeInstanceOf(RangeError);
      }

      const ctx = `iter=${iter} start=${iso(start)} duration=${duration / MIN}min ` +
        `open=${JSON.stringify(open.map((i) => [iso(i.start), iso(i.end)]))} pause=${pauseDays}`;

      if (expected === null) {
        expect(actual, ctx).toBeNull();
        insufficient++;
        continue;
      }
      expect(actual, ctx).toBe(expected);
      if (duration >= 0) forward++;
      else backward++;

      // 证据校验：总时长、落在开放分钟内、按序衔接
      const total = segments.reduce((acc, s) => acc + (s.end - s.start), 0);
      expect(total, ctx).toBe(Math.abs(duration));
      for (const s of segments) {
        expect(s.end, ctx).toBeGreaterThan(s.start);
        for (let t = s.start; t < s.end; t += MIN) {
          expect(grid[(t - ORIGIN) / MIN], ctx).toBe(true);
        }
      }
      if (segments.length > 0) {
        expect(duration >= 0 ? segments[segments.length - 1].end : segments[0].start, ctx).toBe(
          expected,
        );
      }
    }
    // 确保三类场景都被充分覆盖
    console.log(`coverage: forward=${forward} backward=${backward} insufficient=${insufficient}`);
    expect(forward).toBeGreaterThan(200);
    expect(backward).toBeGreaterThan(150);
    expect(insufficient).toBeGreaterThan(100);
  });

  test("逐分钟穷举：固定跨天日历上的所有起点与时长", () => {
    const open: Interval[] = [
      { start: at(1, 9), end: at(1, 17) },
      { start: at(1, 12), end: at(2, 2) }, // 重叠且跨午夜
      { start: at(3, 9), end: at(3, 17) },
      { start: at(4, 9), end: at(4, 17) },
    ];
    const pauseDays = ["2026-01-04"]; // day offset 3 全休
    const grid = minuteCalendar(open, pauseDays, DAYS);
    for (let m = 0; m < 5 * 24 * 60; m += 15) {
      for (const durMin of [-600, -61, -1, 0, 1, 61, 600, 1500]) {
        const start = ORIGIN + m * MIN;
        const duration = durMin * MIN;
        const expected = bruteAdd(grid, start, duration);
        let actual: number | null = null;
        try {
          actual = addWorkTime(start, duration, open, pauseDays).end;
        } catch (e) {
          expect(e).toBeInstanceOf(RangeError);
        }
        expect(actual, `start=${iso(start)} dur=${durMin}min`).toBe(expected);
      }
    }
  });
});
