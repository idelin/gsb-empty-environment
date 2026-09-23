import { defineCompute, Store, Violation } from "../src/index.ts";
import type { Invariant, Op } from "../src/index.ts";

// ---------- 场景：多账户余额 ----------
// 字段：balance:alice / balance:bob / balance:carol、expectedTotal、mode、ledger
// 不变量：单账户余额下界 + 总余额守恒（共享同一个纯计算 totalBalance）。

const BALANCE_FIELDS = ["balance:alice", "balance:bob", "balance:carol"];

// 共享纯计算：两条不变量复用，同一草稿内只求值一次。
const totalBalance = defineCompute("totalBalance", (ctx) =>
  BALANCE_FIELDS.reduce((sum, f) => sum + Number(ctx.get(f)), 0),
);

const nonNegative: Invariant = {
  name: "non-negative-balances",
  deps: (ctx) => {
    for (const f of BALANCE_FIELDS) ctx.get(f);
  },
  check: (ctx) => {
    for (const f of BALANCE_FIELDS) {
      if (Number(ctx.get(f)) < 0) {
        throw new Violation(`${f} 低于下界 0：${ctx.get(f)}`);
      }
    }
  },
};

const totalConserved: Invariant = {
  name: "total-conserved",
  deps: (ctx) => {
    ctx.compute(totalBalance);
    ctx.get("expectedTotal");
  },
  check: (ctx) => {
    const total = ctx.compute(totalBalance); // 与 non-negative 之外的规则共享求值
    if (total !== Number(ctx.get("expectedTotal"))) {
      throw new Violation(`总余额 ${total} 与 expectedTotal ${ctx.get("expectedTotal")} 不一致`);
    }
  },
};

// 依赖随条件分支改变：strict 模式下才依赖 limit 字段。
const withinLimit: Invariant = {
  name: "within-limit",
  deps: (ctx) => {
    ctx.get("mode");
    if (ctx.get("mode") === "strict") {
      ctx.get("limit");
      for (const f of BALANCE_FIELDS) ctx.get(f);
    }
  },
  check: (ctx) => {
    if (ctx.get("mode") !== "strict") return;
    const limit = Number(ctx.get("limit"));
    for (const f of BALANCE_FIELDS) {
      if (Number(ctx.get(f)) > limit) {
        throw new Violation(`${f} 超过 strict 限额 ${limit}`);
      }
    }
  },
};

// 昂贵审计规则：只依赖 ledger，其它字段变更不应触发它。
let auditRuns = 0;
const expensiveAudit: Invariant = {
  name: "expensive-audit",
  deps: (ctx) => {
    ctx.get("ledger");
  },
  check: (ctx) => {
    auditRuns += 1; // 模拟昂贵计算
    if (typeof ctx.get("ledger") !== "string") {
      throw new Violation("ledger 必须是字符串");
    }
  },
};

const store = new Store(
  {
    "balance:alice": 100,
    "balance:bob": 50,
    "balance:carol": 30,
    expectedTotal: 180,
    mode: "normal",
    limit: 120,
    ledger: "genesis",
  },
  { smallTxThreshold: 4 },
);
for (const inv of [nonNegative, totalConserved, withinLimit, expensiveAudit]) {
  store.registerInvariant(inv);
}

const show = (label: string, result: ReturnType<Store["commit"]>) => {
  console.log(`\n=== ${label} ===`);
  console.log(JSON.stringify(result, null, 2));
};

// 1. 合法转账：总额不变、无负余额 -> 提交成功（小事务，逐条全验对照）。
show("合法转账 alice->bob 20", store.commit([
  { kind: "set", field: "balance:alice", value: 80 },
  { kind: "set", field: "balance:bob", value: 70 },
]));

// 2. 非法提交：alice 转负 -> 失败，返回涉及字段、最小相关操作子集与可重放反例。
const bad = store.commit([
  { kind: "set", field: "balance:alice", value: -10 },
  { kind: "set", field: "balance:bob", value: 70 },
  { kind: "set", field: "ledger", value: "tx-2" },
]);
show("非法提交（余额为负）", bad);
if (!bad.ok) {
  const replayed = store.replayCounterexample(bad.failures[0].counterexample);
  console.log("重放反例 ->", replayed.ok ? "未复现（异常）" : `复现违例：${replayed.failures[0].message}`);
}

// 3. 动态依赖：同一事务把 mode 切到 strict 并超限 -> 依赖先在草稿上更新再验证，不能漏检。
show("切 strict 且超限（动态依赖）", store.commit([
  { kind: "set", field: "mode", value: "strict" },
  { kind: "set", field: "balance:alice", value: 150 },
  { kind: "set", field: "balance:carol", value: -40 }, // 同时守恒：80+70+30 -> 150+70-40
]));

// 4. 不相关字段提交（操作数超过小事务阈值，走纯增量路径）：不触发昂贵审计规则。
const before = auditRuns;
show("只改与 ledger 无关的字段（5 个操作，增量验证）", store.commit([
  { kind: "set", field: "limit", value: 200 },
  { kind: "set", field: "ui:theme", value: "dark" },
  { kind: "set", field: "ui:lang", value: "zh" },
  { kind: "set", field: "ui:page", value: 1 },
  { kind: "set", field: "ui:sidebar", value: "open" },
]));
console.log(`expensive-audit 本次执行次数：${auditRuns - before}（0 = 未被触发）`);

// 5. 共享纯计算求值次数。
console.log("\ntotalBalance 总求值次数：", store.computeEvaluations.get("totalBalance"));
console.log("各规则 check 执行次数：", Object.fromEntries(store.checkInvocations));

// 6. 操作日志可回放：把第 1 步的操作在全新仓库上重放，得到相同结果。
const ops: Op[] = [
  { kind: "set", field: "balance:alice", value: 80 },
  { kind: "set", field: "balance:bob", value: 70 },
];
const fresh = new Store({
  "balance:alice": 100, "balance:bob": 50, "balance:carol": 30,
  expectedTotal: 180, mode: "normal", limit: 120, ledger: "genesis",
});
for (const inv of [nonNegative, totalConserved, withinLimit, expensiveAudit]) {
  fresh.registerInvariant(inv);
}
console.log("\n操作日志在全新仓库回放：", fresh.replay(ops).ok ? "成功，结果一致" : "失败（异常）");
