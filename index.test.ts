import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { ContributionBudgetLedger, type Trigger } from "./index.ts";

const MAX = ContributionBudgetLedger.MAX_BUDGET;

function trigger(overrides: Partial<Trigger> = {}): Trigger {
  return {
    sourceId: "source-a",
    sourceType: "navigation",
    dedupKey: "key-1",
    triggerData: 0,
    value: 0,
    ...overrides,
  };
}

describe("边界相加", () => {
  it("恰好加到 65536 被接受", () => {
    const ledger = new ContributionBudgetLedger();
    assert.equal(ledger.register(trigger({ dedupKey: "a", value: 40000 })).accepted, true);
    const r = ledger.register(trigger({ dedupKey: "b", value: 25536 }));
    assert.equal(r.accepted, true);
    assert.equal(r.remainingBudget, 0);
    assert.equal(ledger.contributionSum("source-a"), MAX);
  });

  it("超出预算的贡献整条丢弃、不部分计入", () => {
    const ledger = new ContributionBudgetLedger();
    ledger.register(trigger({ dedupKey: "a", value: MAX - 1 }));
    const r = ledger.register(trigger({ dedupKey: "b", value: 2 }));
    assert.equal(r.accepted, false);
    assert.equal(r.reason, "budget_exceeded");
    assert.equal(ledger.contributionSum("source-a"), MAX - 1);
    assert.equal(r.remainingBudget, 1);
  });

  it("value 恰好等于剩余预算可接受，再加 1 被拒", () => {
    const ledger = new ContributionBudgetLedger();
    assert.equal(ledger.register(trigger({ dedupKey: "a", value: MAX })).accepted, true);
    const r = ledger.register(trigger({ dedupKey: "b", value: 1 }));
    assert.equal(r.accepted, false);
    assert.equal(r.reason, "budget_exceeded");
  });

  it("预算是按来源隔离的", () => {
    const ledger = new ContributionBudgetLedger();
    ledger.register(trigger({ sourceId: "s1", dedupKey: "a", value: MAX }));
    const r = ledger.register(trigger({ sourceId: "s2", dedupKey: "a", value: MAX }));
    assert.equal(r.accepted, true);
  });
});

describe("重复 dedup_key", () => {
  it("同一来源同一 dedupKey 只计一次", () => {
    const ledger = new ContributionBudgetLedger();
    assert.equal(ledger.register(trigger({ dedupKey: "dup", value: 100 })).accepted, true);
    const r = ledger.register(trigger({ dedupKey: "dup", value: 100 }));
    assert.equal(r.accepted, false);
    assert.equal(r.reason, "duplicate_dedup_key");
    assert.equal(ledger.contributionSum("source-a"), 100);
    assert.equal(ledger.reportCount("source-a"), 1);
  });

  it("不同来源的相同 dedupKey 互不影响", () => {
    const ledger = new ContributionBudgetLedger();
    ledger.register(trigger({ sourceId: "s1", dedupKey: "k", value: 10 }));
    const r = ledger.register(trigger({ sourceId: "s2", dedupKey: "k", value: 10 }));
    assert.equal(r.accepted, true);
  });
});

describe("触发数据截断", () => {
  it("导航来源取低 3 位，多余位截断", () => {
    const ledger = new ContributionBudgetLedger();
    const r = ledger.register(trigger({ triggerData: 0b1111, value: 1 }));
    assert.equal(r.triggerData, 0b111);
    assert.equal(ContributionBudgetLedger.truncateTriggerData("navigation", 0b1010), 0b010);
  });

  it("事件来源取低 1 位，多余位截断", () => {
    const ledger = new ContributionBudgetLedger();
    const r = ledger.register(
      trigger({ sourceType: "event", triggerData: 0b110, value: 1 }),
    );
    assert.equal(r.triggerData, 0b0);
    assert.equal(ContributionBudgetLedger.truncateTriggerData("event", 0b111), 0b1);
  });
});

describe("每来源最大报告数", () => {
  it("导航来源最多 3 份报告", () => {
    const ledger = new ContributionBudgetLedger();
    for (let i = 0; i < 3; i++) {
      assert.equal(ledger.register(trigger({ dedupKey: `k${i}`, value: 1 })).accepted, true);
    }
    const r = ledger.register(trigger({ dedupKey: "k3", value: 1 }));
    assert.equal(r.accepted, false);
    assert.equal(r.reason, "report_limit_reached");
    assert.equal(ledger.reportCount("source-a"), 3);
  });

  it("事件来源最多 1 份报告", () => {
    const ledger = new ContributionBudgetLedger();
    assert.equal(
      ledger.register(trigger({ sourceType: "event", dedupKey: "a", value: 1 })).accepted,
      true,
    );
    const r = ledger.register(trigger({ sourceType: "event", dedupKey: "b", value: 1 }));
    assert.equal(r.accepted, false);
    assert.equal(r.reason, "report_limit_reached");
  });
});

describe("并发触发（隐私边界）", () => {
  it("并发提交不越过预算、不重复计数", async () => {
    const ledger = new ContributionBudgetLedger();
    // 同一来源并发 200 条：一半重复 dedupKey，单条 value 30000。
    const jobs = Array.from({ length: 200 }, (_, i) =>
      Promise.resolve().then(() =>
        ledger.register(
          trigger({ dedupKey: `key-${i % 100}`, value: 30000 }),
        ),
      ),
    );
    const results = await Promise.all(jobs);
    const accepted = results.filter((r) => r.accepted);
    // 预算 65536：2 条 30000 入账后余 5536，第 3 条起整条丢弃。
    assert.ok(ledger.contributionSum("source-a") <= MAX);
    assert.equal(ledger.contributionSum("source-a"), accepted.length * 30000);
    assert.equal(accepted.length, 2);
    assert.ok(
      results.every(
        (r) => r.accepted || r.reason === "duplicate_dedup_key" || r.reason === "budget_exceeded",
      ),
    );
  });

  it("并发下同一 dedupKey 只入账一次", async () => {
    const ledger = new ContributionBudgetLedger();
    const jobs = Array.from({ length: 50 }, () =>
      Promise.resolve().then(() =>
        ledger.register(trigger({ dedupKey: "same", value: 7 })),
      ),
    );
    const results = await Promise.all(jobs);
    assert.equal(results.filter((r) => r.accepted).length, 1);
    assert.equal(ledger.contributionSum("source-a"), 7);
  });
});
