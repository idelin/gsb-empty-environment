/**
 * 运行样例：执行器 -> 回放 -> 模型检查 -> 交叉核验 -> 热换规约 -> 夹具布局。
 * 运行：npm run sample
 */

import {
  explore,
  findInvariantViolation,
  findUnreachableStates,
} from "../src/checker.ts";
import { crossValidate } from "../src/crosscheck.ts";
import { badMigration, editorSpecV1, editorSpecV2, migrateV1toV2 } from "../src/editorSpec.ts";
import { Executor, replay } from "../src/executor.ts";
import { dialogTree, glyphMetrics } from "../src/fixtures.ts";
import { layout, nodeIds } from "../src/layout.ts";
import type { Ev, Spec } from "../src/spec.ts";

const line = (s: string) => console.log(s);
const show = (label: string, value: unknown) =>
  console.log(`${label}: ${JSON.stringify(value)}`);

// ---------- 1. 执行器：不变量检查 + 事件录制 ----------
line("== 1. executor ==");
const spec = editorSpecV1();
const ex = new Executor(spec);
ex.registerEffect("persist", () => {});
const tree = { has: (id: string) => nodeIds(dialogTree).has(id) };
ex.attachNodeTree(tree);

const script: Ev[] = [
  { type: "focus", target: "title" },
  { type: "type", payload: { ch: "a" }, target: "status" },
  { type: "type", payload: { ch: "b" } },
  { type: "type", payload: { ch: "c" } },
  { type: "type", payload: { ch: "d" } }, // 守卫拒绝：超出 MAX_LINES
  { type: "save", target: "btn-ok" },
  { type: "lock" },
  { type: "focus" }, // 非法：locked 下无此迁移
  { type: "unlock" },
];
for (const e of script) ex.dispatch(e);
for (const j of ex.journal) {
  const diag = j.diagnostics.length ? ` <- ${j.diagnostics[0]!.code}` : "";
  line(`  #${j.seq} ${j.event.type.padEnd(6)} ${j.status}${diag}  hash=${j.afterHash}`);
}
show("  final", ex.getConfig());

// ---------- 2. 回放：一致性 + 缺目标节点报分歧 ----------
line("== 2. replay ==");
const okReplay = replay(spec, script, {
  recorded: ex.journal,
  nodeTree: tree,
  effects: new Map([["persist", () => {}]]),
});
show("  consistent replay", { finalHash: okReplay.finalHash, divergences: okReplay.divergences });

const brokenTree = { has: (id: string) => id !== "btn-ok" };
const badReplay = replay(spec, script, {
  recorded: ex.journal,
  nodeTree: brokenTree,
  effects: new Map([["persist", () => {}]]),
});
show("  missing-target replay", badReplay.divergences);

// ---------- 3. 模型检查：有限域上的不变量与不可达状态 ----------
line("== 3. model checker ==");
const buggy: Spec = {
  name: "counter",
  version: 1,
  states: [{ id: "root" }],
  initialData: () => ({ n: 0 }),
  transitions: [{ event: "inc", action: (d) => ({ n: (d.n as number) + 1 }) }],
  invariants: [{ name: "n-at-most-2", check: (d) => (d.n as number) <= 2 }],
  eventDomains: { inc: [{}] },
};
const cx = findInvariantViolation(buggy);
show("  shortest counterexample", {
  invariant: cx?.invariant,
  length: cx?.trace.length,
  events: cx?.trace.map((s) => s.event.type),
  final: cx?.finalConfig.data,
});
show("  editor invariants", findInvariantViolation(spec) ?? "hold");
show("  editor unreachable states", findUnreachableStates(spec));
show("  editor reachable space", { states: explore(spec).states.size });

// ---------- 4. 交叉核验：执行器轨迹 vs 检查器一步语义 ----------
line("== 4. cross-validation ==");
show("  crosscheck", crossValidate(spec, script));

// ---------- 5. 热换规约：迁移证据，失败保留旧规约 ----------
line("== 5. hot swap ==");
const swapped = ex.hotSwap(editorSpecV2(), migrateV1toV2);
show("  v1->v2", { ok: swapped.ok, evidence: swapped.evidence });
const rejected = ex.hotSwap(editorSpecV2(), badMigration);
show("  bad migration", { ok: rejected.ok, errors: rejected.errors });
show("  spec still", { name: ex.spec.name, version: ex.spec.version, hash: ex.hash });

// ---------- 6. 夹具布局：不借浏览器的核心计算 ----------
line("== 6. fixture layout ==");
const box = layout(dialogTree, glyphMetrics);
const dump = (b: typeof box, indent: string) => {
  line(`  ${indent}${b.id} @(${b.x},${b.y}) ${b.width}x${b.height}`);
  for (const c of b.children) dump(c, `${indent}  `);
};
dump(box, "");
