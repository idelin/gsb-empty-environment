import assert from "node:assert/strict";
import test from "node:test";
import { defineCompute, Store, Violation } from "../src/index.ts";
import type { Invariant, Op } from "../src/index.ts";

// ---------- 共用夹具 ----------

const BALANCES = ["balance:a", "balance:b"];

function makeStore(options: { smallTxThreshold?: number } = {}) {
  const store = new Store(
    { "balance:a": 100, "balance:b": 50, expectedTotal: 150, mode: "normal", limit: 120, ledger: "g" },
    options,
  );
  const totalBalance = defineCompute("totalBalance", (ctx) =>
    BALANCES.reduce((s, f) => s + Number(ctx.get(f)), 0),
  );
  const nonNegative: Invariant = {
    name: "non-negative",
    deps: (ctx) => { for (const f of BALANCES) ctx.get(f); },
    check: (ctx) => {
      for (const f of BALANCES) {
        if (Number(ctx.get(f)) < 0) throw new Violation(`${f} < 0`);
      }
    },
  };
  const conserved: Invariant = {
    name: "total-conserved",
    deps: (ctx) => { ctx.compute(totalBalance); ctx.get("expectedTotal"); },
    check: (ctx) => {
      if (ctx.compute(totalBalance) !== Number(ctx.get("expectedTotal"))) {
        throw new Violation("total mismatch");
      }
    },
  };
  const withinLimit: Invariant = {
    name: "within-limit",
    deps: (ctx) => {
      ctx.get("mode");
      if (ctx.get("mode") === "strict") {
        ctx.get("limit");
        for (const f of BALANCES) ctx.get(f);
      }
    },
    check: (ctx) => {
      if (ctx.get("mode") !== "strict") return;
      const limit = Number(ctx.get("limit"));
      for (const f of BALANCES) {
        if (Number(ctx.get(f)) > limit) throw new Violation(`${f} > limit`);
      }
    },
  };
  return { store, totalBalance, nonNegative, conserved, withinLimit };
}

const set = (field: string, value: unknown): Op => ({ kind: "set", field, value });

// ---------- 测试 ----------

test("合法提交成功，读者看到新快照", () => {
  const { store, nonNegative, conserved } = makeStore();
  store.registerInvariant(nonNegative);
  store.registerInvariant(conserved);
  const r = store.commit([set("balance:a", 80), set("balance:b", 70)]);
  assert.equal(r.ok, true);
  assert.equal(store.read()["balance:a"], 80);
  assert.equal(store.currentVersion, 1);
});

test("下界与总和约束同时检查，一次失败返回两条违例", () => {
  const { store, nonNegative, conserved } = makeStore();
  store.registerInvariant(nonNegative);
  store.registerInvariant(conserved);
  const r = store.commit([set("balance:a", -10)]);
  assert.equal(r.ok, false);
  if (r.ok) return;
  const rules = r.failures.map((f) => f.rule).sort();
  assert.deepEqual(rules, ["non-negative", "total-conserved"]);
});

test("失败返回涉及字段、最小相关操作子集与可重放反例", () => {
  const { store, nonNegative, conserved } = makeStore();
  store.registerInvariant(nonNegative);
  store.registerInvariant(conserved);
  const r = store.commit([
    set("balance:a", -5),
    set("ledger", "unrelated-op"),
  ]);
  assert.equal(r.ok, false);
  if (r.ok) return;
  const f = r.failures.find((x) => x.rule === "non-negative")!;
  assert.deepEqual(f.fields, ["balance:a", "balance:b"]);
  // 最小相关操作子集：ledger 操作被剔除
  assert.deepEqual(f.relevantOps, [set("balance:a", -5)]);
  assert.deepEqual(f.counterexample, { baseVersion: 0, ops: [set("balance:a", -5)] });
  // 反例可重放：在 baseVersion 快照上复现同一违例
  const replayed = store.replayCounterexample(f.counterexample);
  assert.equal(replayed.ok, false);
  if (!replayed.ok) {
    assert.equal(replayed.failures[0].rule, "non-negative");
  }
});

test("拒绝的提交不留新依赖边、不留半更新状态", () => {
  const { store, nonNegative, withinLimit } = makeStore();
  store.registerInvariant(nonNegative);
  store.registerInvariant(withinLimit);
  const edgesBefore = new Map(
    [...store.dependencyEdges()].map(([k, v]) => [k, new Set(v)]),
  );
  const snapshotBefore = store.read();
  // 该提交若成功会引入 mode/limit 新依赖边并改动状态，但它非法。
  const r = store.commit([
    set("mode", "strict"),
    set("balance:a", -1),
  ]);
  assert.equal(r.ok, false);
  assert.equal(store.read(), snapshotBefore, "状态快照未被半更新");
  assert.equal(store.currentVersion, 0);
  assert.deepEqual(store.dependencyEdges(), edgesBefore, "依赖图未留下新边");
});

test("依赖随条件分支改变：先在草稿上更新实际依赖再验证，不沿用旧依赖漏检", () => {
  const { store, withinLimit } = makeStore();
  store.registerInvariant(withinLimit);
  // 初始 mode=normal，within-limit 只依赖 mode。
  assert.deepEqual([...store.dependencyEdges().get("within-limit")!], ["mode"]);
  // 同事务切 strict 并超限：若沿用旧依赖（仅 mode）推导验证范围会漏掉余额检查。
  const r1 = store.commit([set("mode", "strict"), set("balance:a", 200), set("balance:b", -50)]);
  assert.equal(r1.ok, false);
  if (!r1.ok) {
    assert.ok(r1.failures.some((f) => f.rule === "within-limit"), "within-limit 必须被触发");
  }
  // 分两步：先切 strict（更新依赖边），再只改余额——新依赖边必须让规则触发。
  const ok = store.commit([set("mode", "strict")]);
  assert.equal(ok.ok, true);
  assert.ok(store.dependencyEdges().get("within-limit")!.has("balance:a"));
  const r2 = store.commit([set("balance:a", 200), set("balance:b", -50)]);
  assert.equal(r2.ok, false);
  if (!r2.ok) assert.ok(r2.failures.some((f) => f.rule === "within-limit"));
});

test("多条不变量共享纯计算：同一草稿内只求值一次", () => {
  const { store, totalBalance, nonNegative, conserved } = makeStore();
  // 再加一条也使用 totalBalance 的规则，三条共享。
  const totalPositive: Invariant = {
    name: "total-positive",
    deps: (ctx) => { ctx.compute(totalBalance); },
    check: (ctx) => {
      if (Number(ctx.compute(totalBalance)) <= 0) throw new Violation("total <= 0");
    },
  };
  store.registerInvariant(nonNegative);
  store.registerInvariant(conserved);
  store.registerInvariant(totalPositive);
  const before = store.computeEvaluations.get("totalBalance") ?? 0;
  const r = store.commit([set("balance:a", 60), set("balance:b", 90)]);
  assert.equal(r.ok, true);
  const after = store.computeEvaluations.get("totalBalance") ?? 0;
  assert.equal(after - before, 1, "三条规则共享同一次求值");
});

test("小事务逐条全验对照：依赖声明遗漏也能被全验补获", () => {
  // 一条“说谎”的规则：deps 声明为空，check 实际读取 balance:a。
  const liar: Invariant = {
    name: "liar",
    deps: () => {},
    check: (ctx) => {
      if (Number(ctx.get("balance:a")) < 0) throw new Violation("caught by exhaustive pass");
    },
  };
  const small = new Store({ "balance:a": 1 }, { smallTxThreshold: 8 });
  small.registerInvariant(liar);
  // 热身提交：让规则进入“已验证”集合，之后增量阶段才会因空依赖而跳过它。
  assert.equal(small.commit([set("balance:a", 2)]).ok, true);
  const r1 = small.commit([set("balance:a", -1)]);
  assert.equal(r1.ok, false, "小事务全验对照补获增量漏检");
  if (!r1.ok) assert.equal(r1.failures[0].detectedBy, "exhaustive");
  // 大事务（超过阈值）不做全验，依赖声明遗漏会漏检——对照说明阈值语义。
  const big = new Store({ "balance:a": 1 }, { smallTxThreshold: 0 });
  big.registerInvariant(liar);
  assert.equal(big.commit([set("balance:a", 2)]).ok, true); // 热身
  const r2 = big.commit([set("balance:a", -1)]);
  assert.equal(r2.ok, true);
  if (r2.ok) assert.equal(r2.verified, "incremental");
});

test("不相关字段不触发昂贵规则", () => {
  const { store, nonNegative } = makeStore({ smallTxThreshold: 0 });
  let expensiveRuns = 0;
  const expensive: Invariant = {
    name: "expensive-audit",
    deps: (ctx) => { ctx.get("ledger"); },
    check: () => { expensiveRuns += 1; },
  };
  store.registerInvariant(nonNegative);
  store.registerInvariant(expensive);
  // 热身提交：首次提交会全量验证，先让规则进入“已验证”集合。
  assert.equal(store.commit([set("ledger", "warmup")]).ok, true);
  expensiveRuns = 0;
  const r = store.commit([set("balance:a", 90), set("balance:b", 60)]);
  assert.equal(r.ok, true);
  assert.equal(expensiveRuns, 0, "ledger 未变更，昂贵规则不执行");
});

test("规则自身抛错（非 Violation）使提交失败，kind 为 rule-error", () => {
  const store = new Store({ x: 1 }, { smallTxThreshold: 0 });
  const buggy: Invariant = {
    name: "buggy",
    deps: (ctx) => { ctx.get("x"); },
    check: () => { throw new TypeError("boom"); },
  };
  store.registerInvariant(buggy);
  const r = store.commit([set("x", 2)]);
  assert.equal(r.ok, false);
  if (!r.ok) {
    assert.equal(r.failures[0].kind, "rule-error");
    assert.match(r.failures[0].message, /boom/);
  }
  assert.equal(store.read().x, 1, "状态未变");
});

test("读者不能看到事务中间态", () => {
  const store = new Store({ x: 1, y: 1 }, { smallTxThreshold: 0 });
  let seenDuringCheck: unknown;
  const spy: Invariant = {
    name: "spy",
    deps: (ctx) => { ctx.get("y"); },
    check: () => { seenDuringCheck = store.read().x; },
  };
  store.registerInvariant(spy);
  const r = store.commit([set("x", 999), set("y", 2)]);
  assert.equal(r.ok, true);
  assert.equal(seenDuringCheck, 1, "验证期间读到的仍是已提交旧值");
  assert.equal(store.read().x, 999);
});

test("状态操作可回放：同一操作日志在全新仓库得到相同结果", () => {
  const { store, nonNegative, conserved } = makeStore();
  store.registerInvariant(nonNegative);
  store.registerInvariant(conserved);
  const tx = store.begin();
  tx.set("balance:a", 60).set("balance:b", 90);
  const r1 = tx.commit();
  assert.equal(r1.ok, true);

  const replayEnv = makeStore();
  replayEnv.store.registerInvariant(replayEnv.nonNegative);
  replayEnv.store.registerInvariant(replayEnv.conserved);
  const r2 = replayEnv.store.replay(tx.recordedOps);
  assert.equal(r2.ok, true);
  assert.deepEqual(replayEnv.store.read(), store.read());
});

test("事务草稿 API：begin/set/commit 记录可序列化操作", () => {
  const { store, nonNegative } = makeStore();
  store.registerInvariant(nonNegative);
  const tx = store.begin();
  tx.set("balance:a", 10).set("balance:b", 20);
  assert.deepEqual(tx.recordedOps, [set("balance:a", 10), set("balance:b", 20)]);
  assert.doesNotThrow(() => JSON.stringify(tx.recordedOps), "操作日志可序列化");
  assert.equal(tx.commit().ok, true);
});

test("首次提交验证全部规则（含未被变更字段触及的规则）", () => {
  const { store, nonNegative, conserved } = makeStore();
  store.registerInvariant(nonNegative);
  store.registerInvariant(conserved);
  // 初始状态即非法（expectedTotal 错），首次提交任何字段都必须发现。
  const bad = new Store({ "balance:a": 1, expectedTotal: 999 });
  bad.registerInvariant(conserved);
  const r = bad.commit([set("unrelated", "x")]);
  assert.equal(r.ok, false);
  if (!r.ok) assert.equal(r.failures[0].rule, "total-conserved");
  assert.equal(store.commit([set("unrelated", 1)]).ok, true);
});
