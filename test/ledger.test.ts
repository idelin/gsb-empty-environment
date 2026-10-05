import test from "node:test";
import assert from "node:assert/strict";
import { Frac } from "../src/fraction.ts";
import { listDays } from "../src/dates.ts";
import { Ledger } from "../src/ledger.ts";

// 2024 为闰年：Q1 = 1月(31) + 2月(29) + 3月(31) = 91 天，天然跨月。
const Q_FROM = "2024-01-01";
const Q_TO = "2024-04-01";

function ledgerWithScores(): Ledger {
  const ledger = new Ledger();
  ledger.addRevisions([
    { seq: 1, ruleId: "R1", from: Q_FROM, to: Q_TO, weights: { a: "1/2", b: "1/2" } },
  ]);
  const events = [];
  for (const date of listDays(Q_FROM, Q_TO)) {
    events.push({ date, metric: "a", score: "1" }, { date, metric: "b", score: "0" });
  }
  ledger.addScores(events); // 批量补录
  return ledger;
}

test("分数：约分、精确运算、拒绝浮点", () => {
  assert.equal(Frac.of("2/4").toString(), "1/2");
  assert.equal(Frac.of("1/3").add("1/6").toString(), "1/2");
  assert.equal(Frac.of("0.1").add("0.2").toString(), "3/10"); // 十进制字符串精确解析
  assert.equal(Frac.of("-1/2").mul("-2/3").toString(), "1/3");
  assert.equal(Frac.of(3).div(2).toString(), "3/2");
  assert.ok(Frac.of("1/3").mul(3).equals(Frac.of(1)));
  assert.throws(() => Frac.of(0.1), /浮点/); // 不靠误差容忍，直接拒绝
  assert.throws(() => Frac.of("1/0"), /分母/);
});

test("权重校验：非负且精确和为 1", () => {
  const ledger = new Ledger();
  assert.throws(
    () =>
      ledger.addRevision({
        seq: 1, ruleId: "R", from: "2024-01-01", to: "2024-02-01",
        weights: { a: "1/2", b: "1/3" },
      }),
    /和必须为 1/,
  );
  assert.throws(
    () =>
      ledger.addRevision({
        seq: 1, ruleId: "R", from: "2024-01-01", to: "2024-02-01",
        weights: { a: "1.5", b: "-0.5" },
      }),
    /非负/,
  );
  // 1/3 + 1/3 + 1/3 精确等于 1
  ledger.addRevision({
    seq: 1, ruleId: "R", from: "2024-01-01", to: "2024-02-01",
    weights: { a: "1/3", b: "1/3", c: "1/3" },
  });
  assert.throws(
    () =>
      ledger.addRevision({
        seq: 2, ruleId: "R2", from: "2024-02-01", to: "2024-01-01",
        weights: { a: "1" },
      }),
    /区间为空/,
  );
});

test("跨月季度：季中录入却追溯到季初的修订 + 双时点查询 + 增量对照扫描", () => {
  const ledger = ledgerWithScores();

  const before = ledger.quarterReport(Q_FROM, Q_TO, 1);
  assert.equal(before.days.length, 91);
  assert.equal(before.total?.toString(), "1/2");
  assert.deepEqual(before.segments, [
    { from: Q_FROM, to: Q_TO, ruleId: "R1", ruleSeq: 1 },
  ]);

  ledger.quarterReport(Q_FROM, Q_TO); // 当前视角，填满缓存
  assert.equal(ledger.cachedDates().length, 91);

  // 季中才录入 seq=2，但业务有效期追溯到季初
  ledger.addRevision({
    seq: 2, ruleId: "R2", from: Q_FROM, to: Q_TO, weights: { a: "1/4", b: "3/4" },
  });
  assert.equal(ledger.cachedDates().length, 0); // 受影响区间（整个季度）全部失效

  const { then, now } = ledger.compareQuarter(Q_FROM, Q_TO, 1);
  assert.equal(then.total?.toString(), "1/2"); // 当时看到多少分
  assert.equal(then.segments[0].ruleId, "R1");
  assert.equal(now.total?.toString(), "1/4"); // 按现在规则重算
  assert.deepEqual(now.segments, [
    { from: Q_FROM, to: Q_TO, ruleId: "R2", ruleSeq: 2 },
  ]);

  // 独立逐日扫描对照增量（缓存）结果
  const rescanned = ledger.scanQuarter(Q_FROM, Q_TO);
  assert.deepEqual(now, rescanned);
  assert.deepEqual(ledger.quarterReport(Q_FROM, Q_TO), rescanned); // 缓存命中路径也一致
});

test("撤销最新规则：只在撤销序号之后生效，回退到仍有效的前一版", () => {
  const ledger = ledgerWithScores();
  ledger.addRevision({
    seq: 2, ruleId: "R2", from: Q_FROM, to: Q_TO, weights: { a: "1/4", b: "3/4" },
  });
  ledger.quarterReport(Q_FROM, Q_TO); // 填缓存
  ledger.retract(3, 2); // seq=3 撤销 seq=2
  assert.equal(ledger.cachedDates().length, 0); // 被撤销修订的区间失效

  const asOf2 = ledger.quarterReport(Q_FROM, Q_TO, 2);
  assert.equal(asOf2.segments[0].ruleId, "R2"); // 撤销序号之前：仍看到 R2
  assert.equal(asOf2.total?.toString(), "1/4");

  const now = ledger.quarterReport(Q_FROM, Q_TO);
  assert.deepEqual(now.segments, [
    { from: Q_FROM, to: Q_TO, ruleId: "R1", ruleSeq: 1 }, // 回退前一版
  ]);
  assert.equal(now.total?.toString(), "1/2");
  assert.deepEqual(now, ledger.scanQuarter(Q_FROM, Q_TO));

  assert.throws(() => ledger.retract(4, 2), /已被撤销/);
  assert.throws(() => ledger.retract(5, 999), /不存在/);
});

test("同序号冲突：所有录入共享唯一序号空间", () => {
  const ledger = ledgerWithScores();
  assert.throws(
    () =>
      ledger.addRevision({
        seq: 1, ruleId: "RX", from: Q_FROM, to: Q_TO, weights: { a: "1", b: "0" },
      }),
    /同序号冲突/,
  );
  ledger.addRevision({
    seq: 2, ruleId: "R2", from: Q_FROM, to: Q_TO, weights: { a: "1/4", b: "3/4" },
  });
  assert.throws(() => ledger.retract(2, 1), /同序号冲突/);
  assert.throws(() => ledger.retract(0, 1), /正整数/);
});

test("缺分日期：返回缺失项而不是当 0，季度均值只计完整天", () => {
  const ledger = new Ledger();
  ledger.addRevision({
    seq: 1, ruleId: "R1", from: "2024-01-01", to: "2024-01-04",
    weights: { a: "1/2", b: "1/2" },
  });
  ledger.addScores([
    { date: "2024-01-01", metric: "a", score: "1" },
    { date: "2024-01-01", metric: "b", score: "1" },
    { date: "2024-01-02", metric: "a", score: "1" },
    { date: "2024-01-02", metric: "b", score: "1" },
    { date: "2024-01-03", metric: "a", score: "1" }, // b 缺分
    // 2024-01-04、2024-01-05 无规则覆盖
  ]);

  const report = ledger.quarterReport("2024-01-01", "2024-01-06");
  assert.equal(report.total?.toString(), "1"); // (1+1)/2，缺分天不当 0
  assert.equal(report.completeDays, 2);
  assert.deepEqual(
    report.missingDays.map((d) => d.date),
    ["2024-01-03", "2024-01-04", "2024-01-05"],
  );
  assert.deepEqual(report.missingDays[0].missingMetrics, ["b"]);
  assert.equal(report.missingDays[0].reason, "missing-scores");
  assert.equal(report.missingDays[1].reason, "no-rule");
  assert.deepEqual(report.segments, [
    { from: "2024-01-01", to: "2024-01-04", ruleId: "R1", ruleSeq: 1 },
    { from: "2024-01-04", to: "2024-01-06", ruleId: null, ruleSeq: null },
  ]);
  assert.deepEqual(report, ledger.scanQuarter("2024-01-01", "2024-01-06"));
});

test("缓存：修订与补录只失效受影响区间", () => {
  const ledger = ledgerWithScores();
  ledger.quarterReport(Q_FROM, Q_TO);
  assert.equal(ledger.cachedDates().length, 91);

  // 只覆盖 2 月的修订：1 月、3 月的缓存必须保留
  ledger.addRevision({
    seq: 2, ruleId: "R2", from: "2024-02-01", to: "2024-03-01",
    weights: { a: "1/4", b: "3/4" },
  });
  const remaining = ledger.cachedDates();
  assert.equal(remaining.length, 31 + 31);
  assert.ok(remaining.every((d) => !d.startsWith("2024-02")));

  const now = ledger.quarterReport(Q_FROM, Q_TO);
  assert.deepEqual(now.segments, [
    { from: "2024-01-01", to: "2024-02-01", ruleId: "R1", ruleSeq: 1 },
    { from: "2024-02-01", to: "2024-03-01", ruleId: "R2", ruleSeq: 2 },
    { from: "2024-03-01", to: "2024-04-01", ruleId: "R1", ruleSeq: 1 },
  ]);
  // (62 * 1/2 + 29 * 1/4) / 91 = (153/4) / 91 = 153/364
  assert.equal(now.total?.toString(), "153/364");
  assert.deepEqual(now, ledger.scanQuarter(Q_FROM, Q_TO));

  // 单日补录只失效该日
  ledger.addScore("2024-01-10", "a", "0");
  assert.equal(ledger.cachedDates().length, 90);
  assert.ok(!ledger.cachedDates().includes("2024-01-10"));
});
