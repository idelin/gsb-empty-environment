/**
 * 运行样例：经典 Dangling-Else 文法的 LR(1) 分析表构建与冲突诊断。
 *
 * 运行方式（需要 Node.js >= 22.6）：
 *   node --experimental-strip-types examples/dangling-else.ts
 * 或：
 *   npm run demo
 */

import {
  parseGrammar,
  computeFirst,
  computeFollow,
  buildAutomaton,
  buildTables,
  resolveConflicts,
  diagnoseConflicts,
  formatAutomaton,
  formatTables,
  formatConflictReport,
  formatFirstFollow,
} from "../src/index.ts";

const GRAMMAR = `
# 经典 Dangling-Else 文法
S -> if E then S else S
S -> if E then S
S -> other
E -> cond
`;

function main(): void {
  console.log("=".repeat(64));
  console.log("LR(1) 分析表构建与冲突检测诊断器 —— Dangling-Else 样例");
  console.log("=".repeat(64));

  const grammar = parseGrammar(GRAMMAR);
  console.log("\n文法:");
  for (const p of grammar.productions) {
    console.log(`  (${p.id}) ${p.lhs} -> ${p.rhs.join(" ")}`);
  }
  console.log(`开始符号: ${grammar.start}`);
  console.log(`终结符: { ${[...grammar.terminals].sort().join(", ")} }`);
  console.log(`非终结符: { ${[...grammar.nonterminals].sort().join(", ")} }`);

  const first = computeFirst(grammar);
  const follow = computeFollow(grammar, first);
  console.log("\n" + formatFirstFollow(grammar, first, follow));

  const automaton = buildAutomaton(grammar);
  console.log(`\n规范 LR(1) 项集族：共 ${automaton.states.length} 个状态\n`);
  console.log(formatAutomaton(automaton));

  const tables = buildTables(automaton);
  console.log("\n" + formatTables(automaton, tables));

  console.log(
    `\n冲突检测结果: 共 ${tables.conflicts.length} 处冲突 ` +
      `(Shift/Reduce: ${tables.conflicts.filter((c) => c.type === "shift/reduce").length}, ` +
      `Reduce/Reduce: ${tables.conflicts.filter((c) => c.type === "reduce/reduce").length})`,
  );

  const diagnoses = diagnoseConflicts(automaton, tables);
  for (const d of diagnoses) {
    console.log("\n" + formatConflictReport(d, automaton));
  }

  // 演示冲突消解：SHIFT 优先（else 绑定最近的 if）
  const resolved = resolveConflicts(tables, "shift");
  console.log("\n" + "=".repeat(64));
  console.log('冲突消解演示：resolveConflicts(tables, "shift")');
  console.log("=".repeat(64));
  for (const c of tables.conflicts) {
    const chosen = resolved.action.get(c.state)!.get(c.token)![0];
    console.log(
      `  state ${c.state} [${c.token}] -> ` +
        (chosen.kind === "shift"
          ? `shift ${chosen.state}（else 绑定最近的 if）`
          : JSON.stringify(chosen)),
    );
  }
}

main();
