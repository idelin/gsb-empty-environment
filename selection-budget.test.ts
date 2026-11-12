import { describe, test } from "node:test";
import assert from "node:assert/strict";
import {
  DEFAULT_WINDOW_MS,
  SelectionBudget,
} from "./selection-budget.ts";

const SITE = "https://top.example";
const ORIGIN = "https://worklet.example";

function makeClock(start = 1_000_000) {
  let t = start;
  return {
    now: () => t,
    set: (ms: number) => {
      t = ms;
    },
    advance: (ms: number) => {
      t += ms;
    },
  };
}

function makeBudget(clock = makeClock()) {
  return { clock, budget: new SelectionBudget({ now: clock.now }) };
}

describe("候选数校验", () => {
  test("n 在 2 到 8 之间时正常选择", () => {
    for (let n = 2; n <= 8; n += 1) {
      const { budget } = makeBudget();
      assert.equal(budget.select(SITE, ORIGIN, n, () => n - 1), n - 1);
    }
  });

  test("n 非法时抛 RangeError 且不扣费", () => {
    const { budget } = makeBudget();
    const invalid = [0, 1, 9, -2, 2.5, 7.999, NaN, Infinity, -Infinity, "3", undefined, null];
    for (const n of invalid) {
      assert.throws(
        () => budget.select(SITE, ORIGIN, n as unknown as number, () => 0),
        RangeError,
        `n=${String(n)} 应抛错`,
      );
    }
    assert.equal(budget.remainingBits(SITE, ORIGIN), 12);
  });

  test("choose 返回越界下标时抛错且不扣费", () => {
    const { budget } = makeBudget();
    for (const bad of [-1, 4, 1.5, NaN]) {
      assert.throws(() => budget.select(SITE, ORIGIN, 4, () => bad), RangeError);
    }
    assert.equal(budget.remainingBits(SITE, ORIGIN), 12);
  });
});

describe("比特预算", () => {
  test("12 次 n=2 恰好耗尽 12 比特，第 13 次返回 0 且不扣费", () => {
    const { budget } = makeBudget();
    let chooserCalls = 0;
    const choose = () => {
      chooserCalls += 1;
      return 1;
    };
    for (let i = 0; i < 12; i += 1) {
      assert.equal(budget.select(SITE, ORIGIN, 2, choose), 1);
    }
    assert.equal(budget.remainingBits(SITE, ORIGIN), 0);
    assert.equal(budget.select(SITE, ORIGIN, 2, choose), 0);
    assert.equal(chooserCalls, 12, "余额不足时不应调用 choose");
    assert.equal(budget.remainingBits(SITE, ORIGIN), 0, "拒绝时不扣费");
  });

  test("4 次 n=8 恰好耗尽（4 * log2(8) = 12）", () => {
    const { budget } = makeBudget();
    for (let i = 0; i < 4; i += 1) {
      assert.equal(budget.select(SITE, ORIGIN, 8, () => 7), 7);
    }
    assert.equal(budget.select(SITE, ORIGIN, 2, () => 1), 0);
  });

  test("余额不足返回第 0 个候选，剩余额度仍可用于更便宜的选择", () => {
    const { budget } = makeBudget();
    budget.select(SITE, ORIGIN, 8, () => 1); // 3 比特
    budget.select(SITE, ORIGIN, 8, () => 1); // 3 比特
    budget.select(SITE, ORIGIN, 8, () => 1); // 3 比特
    budget.select(SITE, ORIGIN, 4, () => 1); // 2 比特，共 11
    assert.equal(budget.select(SITE, ORIGIN, 4, () => 1), 0, "2 比特余额不足");
    assert.equal(budget.remainingBits(SITE, ORIGIN), 1);
    assert.equal(budget.select(SITE, ORIGIN, 2, () => 1), 1, "1 比特仍可支付");
    assert.equal(budget.remainingBits(SITE, ORIGIN), 0);
  });

  test("不同（顶层站点, worklet 源）的预算相互独立", () => {
    const { budget } = makeBudget();
    for (let i = 0; i < 4; i += 1) budget.select(SITE, ORIGIN, 8, () => 1);
    assert.equal(budget.select(SITE, ORIGIN, 2, () => 1), 0);
    assert.equal(budget.select("https://other.example", ORIGIN, 2, () => 1), 1);
    assert.equal(budget.select(SITE, "https://other-worklet.example", 2, () => 1), 1);
  });
});

describe("浮点误差", () => {
  test("非 2 的幂候选数按精确乘积记账，不受 log2 浮点累加影响", () => {
    const { budget } = makeBudget();
    // 7 * log2(3) ≈ 11.0947 <= 12，但 3^8 = 6561 > 4096，第 8 次必须拒绝。
    for (let i = 0; i < 7; i += 1) {
      assert.notEqual(budget.select(SITE, ORIGIN, 3, () => 2), 0);
    }
    assert.equal(budget.select(SITE, ORIGIN, 3, () => 2), 0);
    // 剩余 ≈ 0.905 比特 < 1，连 n=2 也支付不起（3^7 * 2 = 4374 > 4096）。
    assert.equal(budget.select(SITE, ORIGIN, 2, () => 1), 0);
  });

  test("混合候选数：6 次 n=3 后可再付两次 n=2，第三次拒绝", () => {
    const { budget } = makeBudget();
    for (let i = 0; i < 6; i += 1) budget.select(SITE, ORIGIN, 3, () => 1);
    // 3^6 * 2 * 2 = 2916 <= 4096；再乘 2 得 5832 > 4096。
    assert.equal(budget.select(SITE, ORIGIN, 2, () => 1), 1);
    assert.equal(budget.select(SITE, ORIGIN, 2, () => 1), 1);
    assert.equal(budget.select(SITE, ORIGIN, 2, () => 1), 0);
  });

  test("恰好等于 12 比特时允许，再小的超额也拒绝", () => {
    const { budget } = makeBudget();
    budget.select(SITE, ORIGIN, 8, () => 1); // 3 比特
    for (let i = 0; i < 9; i += 1) budget.select(SITE, ORIGIN, 2, () => 1); // 9 比特
    assert.equal(budget.remainingBits(SITE, ORIGIN), 0);
    assert.equal(budget.select(SITE, ORIGIN, 2, () => 1), 0);
  });
});

describe("滑动窗口", () => {
  test("扣费在窗口结束前 1 毫秒仍占用额度", () => {
    const clock = makeClock();
    const budget = new SelectionBudget({ now: clock.now });
    budget.select(SITE, ORIGIN, 8, () => 1); // t0 扣 3 比特
    clock.advance(DEFAULT_WINDOW_MS - 1);
    assert.equal(budget.remainingBits(SITE, ORIGIN), 9);
  });

  test("扣费在恰好满 24 小时时按毫秒精确到期", () => {
    const clock = makeClock();
    const budget = new SelectionBudget({ now: clock.now });
    budget.select(SITE, ORIGIN, 8, () => 1);
    clock.advance(DEFAULT_WINDOW_MS);
    assert.equal(budget.remainingBits(SITE, ORIGIN), 12);
    assert.equal(budget.select(SITE, ORIGIN, 8, () => 1), 1);
  });

  test("窗口滑动：各笔扣费按各自到期时刻分别释放额度", () => {
    const clock = makeClock(0);
    const budget = new SelectionBudget({ now: clock.now });
    budget.select(SITE, ORIGIN, 8, () => 1); // t=0，3 比特
    clock.set(60_000);
    budget.select(SITE, ORIGIN, 8, () => 1); // t=60s，3 比特
    clock.set(120_000);
    budget.select(SITE, ORIGIN, 8, () => 1); // t=120s，3 比特
    budget.select(SITE, ORIGIN, 8, () => 1); // t=120s，3 比特，共 12
    assert.equal(budget.select(SITE, ORIGIN, 2, () => 1), 0, "预算已满");

    clock.set(DEFAULT_WINDOW_MS); // 第一笔到期
    assert.equal(budget.remainingBits(SITE, ORIGIN), 3);
    assert.equal(budget.select(SITE, ORIGIN, 4, () => 1), 1, "释放 3 比特可付 2 比特");
    assert.equal(budget.select(SITE, ORIGIN, 4, () => 1), 0, "只剩 1 比特，付不起 2 比特");

    clock.set(DEFAULT_WINDOW_MS + 60_000); // 第二笔到期
    assert.equal(budget.remainingBits(SITE, ORIGIN), 4);
  });

  test("自定义窗口长度同样按毫秒精确到期", () => {
    const clock = makeClock(0);
    const budget = new SelectionBudget({ now: clock.now, windowMs: 1_000, maxBits: 3 });
    budget.select(SITE, ORIGIN, 8, () => 1); // 恰好 3 比特
    assert.equal(budget.select(SITE, ORIGIN, 2, () => 1), 0);
    clock.set(999);
    assert.equal(budget.select(SITE, ORIGIN, 2, () => 1), 0);
    clock.set(1_000);
    assert.equal(budget.select(SITE, ORIGIN, 2, () => 1), 1);
  });
});

describe("并发结算", () => {
  test("同步突发调用按到达顺序结算，超预算者返回 0", () => {
    const { budget } = makeBudget();
    let chooserCalls = 0;
    const results = Array.from({ length: 5 }, () =>
      budget.select(SITE, ORIGIN, 8, () => {
        chooserCalls += 1;
        return 5;
      }),
    );
    assert.deepEqual(results, [5, 5, 5, 5, 0]);
    assert.equal(chooserCalls, 4);
  });

  test("异步并发按实际到达顺序结算，而非任务创建顺序", async () => {
    const { budget } = makeBudget();
    const gates: Array<() => void> = [];
    const tasks = Array.from({ length: 5 }, (_, i) =>
      (async () => {
        await new Promise<void>((resolve) => gates.push(resolve));
        return budget.select(SITE, ORIGIN, 8, () => i + 1);
      })(),
    );
    // 按 2, 0, 4, 1, 3 的顺序放行：前四个各扣 3 比特，最后放行的任务 3 应返回 0。
    for (const i of [2, 0, 4, 1, 3]) {
      gates[i]();
      await Promise.resolve();
      await Promise.resolve();
    }
    const results = await Promise.all(tasks);
    assert.deepEqual(results, [1, 2, 3, 0, 5]);
    assert.equal(budget.remainingBits(SITE, ORIGIN), 0);
  });

  test("Promise.all 并发：恰好 4 个 n=8 成功，其余返回 0", async () => {
    const { budget } = makeBudget();
    const results = await Promise.all(
      Array.from({ length: 10 }, (_, i) =>
        Promise.resolve().then(() => budget.select(SITE, ORIGIN, 8, () => (i % 7) + 1)),
      ),
    );
    assert.equal(results.filter((r) => r !== 0).length, 4);
    assert.deepEqual(results.slice(4), [0, 0, 0, 0, 0, 0]);
  });
});

describe("构造参数", () => {
  test("默认构造使用真实时钟，可正常选择", () => {
    const budget = new SelectionBudget();
    const index = budget.select(SITE, ORIGIN, 4, () => 3);
    assert.equal(index, 3);
    assert.equal(budget.remainingBits(SITE, ORIGIN), 10);
  });

  test("非法 windowMs / maxBits 抛 RangeError", () => {
    assert.throws(() => new SelectionBudget({ windowMs: 0 }), RangeError);
    assert.throws(() => new SelectionBudget({ windowMs: -1 }), RangeError);
    assert.throws(() => new SelectionBudget({ windowMs: NaN }), RangeError);
    assert.throws(() => new SelectionBudget({ maxBits: 0 }), RangeError);
    assert.throws(() => new SelectionBudget({ maxBits: 1.5 }), RangeError);
  });
});
