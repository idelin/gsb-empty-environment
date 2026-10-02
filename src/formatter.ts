/**
 * 项集、状态、分析表与冲突报告的可读格式化。
 */

import { END, EPSILON, formatProduction, type Grammar } from "./grammar.ts";
import type { Automaton, Item, State } from "./lr1.ts";
import { formatAction, type ParseTables } from "./table.ts";
import type { ConflictDiagnosis } from "./conflict.ts";

const DOT = "•";
const ARROW = "→";

/** 单个 LR(1) 项的可读形式，如 `S → if E then S • else S` */
export function formatItem(item: Item, g: Grammar): string {
  const prod = g.productions[item.prod];
  const rhs = prod.rhs.length === 0 ? [EPSILON] : prod.rhs;
  const dotPos = prod.rhs.length === 0 ? 1 : item.dot;
  const parts = [...rhs.slice(0, dotPos), DOT, ...rhs.slice(dotPos)];
  return `${prod.lhs} ${ARROW} ${parts.join(" ")}`;
}

/** 将同 (prod, dot) 的项合并前瞻符号显示，如 `[ $, else ]` */
function mergeLookaheads(items: Item[]): { item: Item; lookaheads: string[] }[] {
  const map = new Map<string, { item: Item; lookaheads: string[] }>();
  for (const it of items) {
    const key = `${it.prod}:${it.dot}`;
    const entry = map.get(key);
    if (entry) entry.lookaheads.push(it.lookahead);
    else map.set(key, { item: it, lookaheads: [it.lookahead] });
  }
  return [...map.values()];
}

function formatItemGroup(items: Item[], g: Grammar, indent: string): string[] {
  return mergeLookaheads(items).map(({ item, lookaheads }) => {
    const las = [...new Set(lookaheads)].sort().join(", ");
    return `${indent}${formatItem(item, g)}   [${las}]`;
  });
}

/** 单个状态的完整描述（内核项 + 闭包项 + 转移） */
export function formatState(state: State, g: Grammar): string {
  const kernelKeys = new Set(state.kernel.map((it) => `${it.prod}:${it.dot}:${it.lookahead}`));
  const closureOnly = state.items.filter(
    (it) => !kernelKeys.has(`${it.prod}:${it.dot}:${it.lookahead}`),
  );
  const lines: string[] = [`State ${state.id}:`];
  for (const l of formatItemGroup(state.kernel, g, "  ")) lines.push(l);
  if (closureOnly.length > 0) {
    lines.push("  " + "-".repeat(28));
    for (const l of formatItemGroup(closureOnly, g, "  ")) lines.push(l);
  }
  if (state.transitions.size > 0) {
    lines.push("  " + "-".repeat(28));
    for (const [sym, target] of [...state.transitions.entries()].sort()) {
      lines.push(`  ${sym} ${ARROW} state ${target}`);
    }
  }
  return lines.join("\n");
}

/** 全部状态的项集报告 */
export function formatAutomaton(automaton: Automaton): string {
  return automaton.states.map((s) => formatState(s, automaton.grammar)).join("\n\n");
}

/** ACTION/GOTO 表的可读形式 */
export function formatTables(automaton: Automaton, tables: ParseTables): string {
  const g = automaton.grammar;
  const prodText = (id: number) => formatProduction(g, id);
  const conflictCells = new Set(tables.conflicts.map((c) => `${c.state}:${c.token}`));
  const lines: string[] = ["ACTION 表:"];
  const stateIds = [...tables.action.keys()].sort((a, b) => a - b);
  for (const sid of stateIds) {
    const row = tables.action.get(sid)!;
    for (const [token, actions] of [...row.entries()].sort()) {
      const text = actions.map((a) => formatAction(a, prodText)).join("  |  ");
      const mark = conflictCells.has(`${sid}:${token}`) ? "   <<< 冲突" : "";
      lines.push(`  state ${String(sid).padStart(2)}  [${token}]  ${ARROW}  ${text}${mark}`);
    }
  }
  lines.push("", "GOTO 表:");
  const gotoStateIds = [...tables.goto.keys()].sort((a, b) => a - b);
  for (const sid of gotoStateIds) {
    const row = tables.goto.get(sid)!;
    for (const [nt, target] of [...row.entries()].sort()) {
      lines.push(`  state ${String(sid).padStart(2)}  [${nt}]  ${ARROW}  state ${target}`);
    }
  }
  return lines.join("\n");
}

/** 单个冲突的完整诊断报告 */
export function formatConflictReport(
  diagnosis: ConflictDiagnosis,
  automaton: Automaton,
): string {
  const g = automaton.grammar;
  const { conflict, evidence, verified, suggestions } = diagnosis;
  const prodText = (id: number) => formatProduction(g, id);
  const title =
    conflict.type === "shift/reduce" ? "Shift/Reduce 冲突" : "Reduce/Reduce 冲突";
  const bar = "=".repeat(64);
  const lines: string[] = [bar, `冲突报告: ${title}`, bar];
  lines.push(`状态: ${conflict.state}`);
  lines.push(`触发 token: ${conflict.token}`);
  lines.push("");
  lines.push(`在状态 ${conflict.state} 下遇到 ${conflict.token} 时，以下动作均合法:`);
  for (const action of conflict.actions) {
    lines.push(`  - ${formatAction(action, prodText)}`);
  }
  lines.push("");
  lines.push("引发冲突的 LR(1) 项:");
  for (const item of conflict.items) {
    lines.push(`  ${formatItem(item, g)}   [${item.lookahead}]`);
  }
  lines.push("");
  if (evidence.length > 0) {
    lines.push(`最短输入证据（${evidence.length} 个 token）:`);
    lines.push(`  ${evidence.join(" ")}`);
    if (conflict.token === END) {
      lines.push(
        `  解析器读完 "${evidence.join(" ")}" 后处于状态 ${conflict.state}，` +
          `输入结束（$）时移进与归约（或两条归约）均合法。`,
      );
    } else {
      const consumed = evidence.slice(0, -1).join(" ");
      lines.push(
        `  解析器读完 "${consumed}" 后处于状态 ${conflict.state}，` +
          `下一个 token 为 ${conflict.token} 时移进与归约（或两条归约）均合法。`,
      );
    }
  } else {
    lines.push("最短输入证据: <无法生成>");
  }
  lines.push(`证据模拟验证: ${verified ? "通过（非确定性模拟可复现冲突点）" : "未通过"}`);
  if (conflict.type === "shift/reduce") {
    lines.push("");
    lines.push("两种选择的含义:");
    lines.push(`  - SHIFT : ${conflict.token} 绑定到最近（内层）的未完结结构`);
    lines.push(`  - REDUCE: 先归约内层结构，${conflict.token} 将归属外层结构`);
  }
  lines.push("");
  lines.push("解决建议:");
  suggestions.forEach((s, i) => lines.push(`  ${i + 1}. ${s}`));
  return lines.join("\n");
}

/** First/Follow 集的可读形式 */
export function formatFirstFollow(
  g: Grammar,
  first: Map<string, Set<string>>,
  follow: Map<string, Set<string>>,
): string {
  const lines: string[] = ["First 集:"];
  for (const nt of [...g.nonterminals].sort()) {
    lines.push(`  First(${nt}) = { ${[...first.get(nt)!].sort().join(", ")} }`);
  }
  lines.push("Follow 集:");
  for (const nt of [...g.nonterminals].sort()) {
    lines.push(`  Follow(${nt}) = { ${[...follow.get(nt)!].sort().join(", ")} }`);
  }
  return lines.join("\n");
}

export { END };
