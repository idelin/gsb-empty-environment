// 演示 + 自测：
//   S1 共享子树的逻辑复制（原地改动会污染，这里验证不污染）
//   S2 规则优先级
//   S3 替换式引入新共享（double(x) -> add(x, x)）
//   S4 重复状态检测与循环报告
//   S5 预算中断、恢复与跨进程快照
//   S6 随机项上 DAG 重写器 vs 无共享朴素解释器的逐步核对

import { Factory, Node, p, pretty, v } from "./terms";
import { Rewriter, Rule, TraceStep, exportState, importState } from "./rewriter";
import { crossCheck } from "./check";

let failures = 0;
function check(name: string, cond: boolean, detail = ""): void {
  if (cond) {
    console.log(`  ok   ${name}`);
  } else {
    failures++;
    console.error(`  FAIL ${name}${detail ? " -- " + detail : ""}`);
  }
}

function printSteps(steps: readonly TraceStep[]): void {
  for (const s of steps) {
    const binds = Object.entries(s.bindings)
      .map(([k, val]) => `${k} := ${val}`)
      .join(", ");
    console.log(
      `  step ${s.step}  @[${s.path.join(",")}]  ${s.rule}\n` +
        `         ${s.redex}  ~>  ${s.replacement}    { ${binds} }`,
    );
  }
}

// 化简规则集（终止）：优先级数值小者先尝试。
const simpRules: Rule[] = [
  { name: "add-0-l", priority: 0, lhs: p("add", p("0"), v("x")), rhs: v("x") },
  { name: "add-0-r", priority: 0, lhs: p("add", v("x"), p("0")), rhs: v("x") },
  { name: "mul-0-l", priority: 0, lhs: p("mul", p("0"), v("x")), rhs: p("0") },
  { name: "mul-0-r", priority: 0, lhs: p("mul", v("x"), p("0")), rhs: p("0") },
  { name: "mul-1-l", priority: 0, lhs: p("mul", p("1"), v("x")), rhs: v("x") },
  { name: "mul-1-r", priority: 0, lhs: p("mul", v("x"), p("1")), rhs: v("x") },
  { name: "neg-neg", priority: 0, lhs: p("neg", p("neg", v("x"))), rhs: v("x") },
  { name: "double", priority: 1, lhs: p("double", v("x")), rhs: p("add", v("x"), v("x")) },
];

// ---------- S1 共享子树：逻辑复制，不污染其他位置 ----------
console.log("S1 shared subtree is logically copied, never mutated in place");
{
  const f = new Factory();
  const a = f.mk("a");
  const shared = f.mk("add", [f.mk("0"), a]); // 被两个位置共享
  const root = f.mk("mul", [shared, shared]);
  const rw = new Rewriter(f, simpRules);

  const first = rw.run(rw.init(root), 1);
  check("leftmost-innermost hits left occurrence first",
    first.steps[0].rule === "add-0-l" && first.steps[0].path.join(",") === "0");
  check("right occurrence keeps the ORIGINAL shared node (no pollution)",
    first.state.root.args[1] === shared);
  check("left occurrence is a NEW node", first.state.root.args[0] === a);
  console.log(`  trace for ${pretty(root)}:`);
  const rest = rw.run(first.state, 100);
  printSteps([...first.steps, ...rest.steps]);
  check("reaches normal form mul(a, a)",
    rest.state.status === "normal-form" && pretty(rest.state.root) === "mul(a, a)");

  const cc = crossCheck(new Factory(), simpRules, root, 100);
  check("naive interpreter agrees on the whole sequence", cc.ok, cc.mismatch);
}

// ---------- S2 优先级 ----------
console.log("S2 rules are tried by priority");
{
  const f = new Factory();
  const rules: Rule[] = [
    { name: "add-comm", priority: 9, lhs: p("add", v("x"), v("y")), rhs: p("add", v("y"), v("x")) },
    simpRules[0], // add-0-l, priority 0
  ];
  const root = f.mk("add", [f.mk("0"), f.mk("b")]);
  const rw = new Rewriter(f, rules);
  const out = rw.run(rw.init(root), 50);
  check("high-priority add-0-l fires (not the looping commute rule)",
    out.steps.length === 1 && out.steps[0].rule === "add-0-l"
    && out.state.status === "normal-form" && pretty(out.state.root) === "b");
  const cc = crossCheck(new Factory(), rules, root, 50);
  check("naive interpreter agrees", cc.ok, cc.mismatch);
}

// ---------- S3 替换式引入共享 ----------
console.log("S3 template instantiation shares bound subtrees by reference");
{
  const f = new Factory();
  const c = f.mk("c");
  const root = f.mk("double", [c]);
  const rw = new Rewriter(f, simpRules);
  const out = rw.run(rw.init(root), 1);
  check("double(c) -> add(c, c)", pretty(out.state.root) === "add(c, c)");
  check("both children are the SAME object (shared, not duplicated)",
    out.state.root.args[0] === out.state.root.args[1] && out.state.root.args[0] === c);
}

// ---------- S4 循环检测 ----------
console.log("S4 repeated states are detected and reported as a cycle");
{
  const f = new Factory();
  const rules: Rule[] = [
    { name: "f-to-g", priority: 0, lhs: p("f", v("x")), rhs: p("g", v("x")) },
    { name: "g-to-f", priority: 1, lhs: p("g", v("x")), rhs: p("f", v("x")) },
  ];
  const root = f.mk("f", [f.mk("a")]);
  const rw = new Rewriter(f, rules);
  const out = rw.run(rw.init(root), 1000);
  check("cycle reported", out.state.status === "cycle");
  check("cycle span is steps 0..2 (f(a) repeats)",
    !!out.state.cycle && out.state.cycle.from === 0 && out.state.cycle.to === 2,
    JSON.stringify(out.state.cycle));
  console.log(`  cycle: state at step ${out.state.cycle!.from} reappears at step ${out.state.cycle!.to}`);
  const cc = crossCheck(new Factory(), rules, root, 1000);
  check("naive interpreter reports the same cycle", cc.ok, cc.mismatch);
}

// ---------- S5 预算中断 / 恢复 / 快照 ----------
console.log("S5 budget interruption is resumable (in-memory and via snapshot)");
{
  const f = new Factory();
  const t = (sym: string, ...args: Node[]) => f.mk(sym, args);
  const root = t("add",
    t("mul", t("1"), t("double", t("a"))),
    t("mul", t("0"), t("neg", t("neg", t("b")))));

  const full = new Rewriter(f, simpRules).run(
    new Rewriter(f, simpRules).init(root), 1000);
  check("uninterrupted run terminates", full.state.status === "normal-form");

  // 分段恢复：每次 2 步
  const rw = new Rewriter(f, simpRules);
  let state = rw.init(root);
  const chunked: TraceStep[] = [];
  while (state.status === "running" || state.status === "budget") {
    const out = rw.run(state, 2);
    chunked.push(...out.steps);
    state = out.state;
  }
  check("chunked run reaches the same normal form",
    state.status === "normal-form" && pretty(state.root) === pretty(full.state.root));
  check("chunked trace equals uninterrupted trace",
    chunked.length === full.steps.length
    && chunked.every((s, i) => s.rule === full.steps[i].rule
      && s.path.join(",") === full.steps[i].path.join(",")));

  // 快照：中断后导出，换全新工厂导入再恢复（模拟跨进程）
  const rw2 = new Rewriter(f, simpRules);
  const half = rw2.run(rw2.init(root), 1);
  check("budget status reported", half.state.status === "budget");
  const snapshot = JSON.stringify(exportState(f, half.state));
  const f2 = new Factory();
  const restored = importState(f2, JSON.parse(snapshot));
  const rw3 = new Rewriter(f2, simpRules);
  const tail = rw3.run(restored, 1000);
  check("snapshot resume reaches the same normal form",
    tail.state.status === "normal-form" && pretty(tail.state.root) === pretty(full.state.root));
  check("step numbering continues across snapshot",
    tail.steps.length === full.steps.length - 1
    && tail.steps.every((s, i) => s.rule === full.steps[i + 1].rule));
}

// ---------- S6 随机项核对 ----------
console.log("S6 fuzz: DAG rewriter vs naive no-sharing interpreter");
{
  let seed = 0xC0FFEE;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  const atoms = ["0", "1", "a", "b", "c"];
  const unary = ["neg", "double"];
  const binary = ["add", "mul"];
  const f = new Factory();
  const gen = (depth: number): Node => {
    if (depth === 0 || rand() < 0.35) return f.mk(atoms[(rand() * atoms.length) | 0]);
    if (rand() < 0.4) return f.mk(unary[(rand() * unary.length) | 0], [gen(depth - 1)]);
    return f.mk(binary[(rand() * binary.length) | 0], [gen(depth - 1), gen(depth - 1)]);
  };
  let okCount = 0;
  const N = 60;
  for (let i = 0; i < N; i++) {
    const term = gen(4);
    const chunk = [0, 1, 3][i % 3]; // 1/3 的用例分段恢复执行
    const cc = crossCheck(f, simpRules, term, 1000, chunk);
    if (!cc.ok) {
      check(`fuzz case ${i} (${pretty(term)})`, false, cc.mismatch);
    } else {
      okCount++;
    }
  }
  check(`all ${N} random terms agree step-by-step`, okCount === N, `${okCount}/${N}`);
}

console.log(failures === 0 ? "\nALL CHECKS PASSED" : `\n${failures} CHECK(S) FAILED`);
process.exit(failures === 0 ? 0 : 1);
