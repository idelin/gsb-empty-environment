import type { Grammar } from "./grammar.ts";
import { formatAction, formatItem } from "./lr1.ts";
import type { Conflict, LR1Table } from "./lr1.ts";

/** Shortest terminal-string expansion for every non-terminal (fixed point). */
function shortestExpansions(g: Grammar): Map<string, string[]> {
  const best = new Map<string, string[]>();
  let changed = true;
  while (changed) {
    changed = false;
    for (const p of g.productions) {
      const parts: string[][] = [[]];
      let ok = true;
      for (const sym of p.rhs) {
        if (g.terminals.has(sym)) {
          for (const part of parts) part.push(sym);
        } else if (best.has(sym)) {
          const exp = best.get(sym)!;
          for (const part of parts) part.push(...exp);
        } else {
          ok = false;
          break;
        }
      }
      if (!ok) continue;
      const candidate = parts[0];
      const current = best.get(p.lhs);
      if (current === undefined || candidate.length < current.length) {
        best.set(p.lhs, candidate);
        changed = true;
      }
    }
  }
  return best;
}

/** BFS over the automaton from state 0 to `target`, returning the symbols
 *  labelling a shortest path. */
function shortestPathTo(table: LR1Table, target: number): string[] | null {
  const visited = new Set<number>([0]);
  const queue: Array<{ state: number; path: string[] }> = [
    { state: 0, path: [] },
  ];
  while (queue.length > 0) {
    const { state, path } = queue.shift()!;
    if (state === target) return path;
    for (const [sym, next] of table.transitions[state]) {
      if (!visited.has(next)) {
        visited.add(next);
        queue.push({ state: next, path: [...path, sym] });
      }
    }
  }
  return null;
}

/** Shortest concrete input token string that drives the parser into the
 *  conflict state and exposes the conflicting lookahead token. */
export function conflictEvidence(table: LR1Table, conflict: Conflict): string[] {
  const expansions = shortestExpansions(table.grammar);
  const path = shortestPathTo(table, conflict.state) ?? [];
  const tokens: string[] = [];
  for (const sym of path) {
    if (table.grammar.terminals.has(sym)) {
      tokens.push(sym);
    } else {
      const exp = expansions.get(sym);
      if (exp) tokens.push(...exp);
    }
  }
  tokens.push(conflict.token);
  return tokens;
}

function resolutionAdvice(conflict: Conflict): string[] {
  if (conflict.kind === "shift/reduce") {
    return [
      "若希望 else 绑定到最近的 if（常见语义）：选择移进（shift），即 Yacc/Bison 的默认行为。",
      "在生成器中为终结符声明优先级/结合性（如 %nonassoc），让工具自动消解。",
      "或重写文法消除二义性：引入 matched/unmatched 语句两级产生式（无二义文法）。",
    ];
  }
  return [
    "两条产生式在相同向前看符号下都可归约，文法对该前缀存在二义。",
    "合并或区分可归约的产生式，或增加向前看（改用 LR(k), k>1）/ 改写文法。",
  ];
}

/** Human-readable diagnostic report for every detected conflict. */
export function diagnose(table: LR1Table): string {
  const lines: string[] = [];
  if (table.conflicts.length === 0) {
    lines.push("未检测到冲突：该文法是 LR(1) 文法。");
    return lines.join("\n");
  }
  for (const c of table.conflicts) {
    const g = table.grammar;
    lines.push(
      `冲突 #${table.conflicts.indexOf(c) + 1}: ${c.kind} 冲突 ` +
        `（状态 ${c.state}，向前看符号 '${c.token}'）`,
    );
    lines.push("");
    lines.push(`  状态 ${c.state} 的 LR(1) 项目集：`);
    for (const it of table.states[c.state]) {
      lines.push(`    ${formatItem(g, it)}`);
    }
    lines.push("");
    lines.push(`  遇到 '${c.token}' 时以下动作均合法：`);
    for (const a of c.actions) {
      lines.push(`    - ${formatAction(g, a)}`);
    }
    lines.push("");
    const evidence = conflictEvidence(table, c);
    lines.push(`  最短冲突证据输入串： ${evidence.join(" ")}`);
    lines.push(`    （前 ${evidence.length - 1} 个 token 将分析器驱动至状态 ` +
      `${c.state}，此时读入 '${c.token}' 触发冲突）`);
    lines.push("");
    lines.push("  解决建议：");
    for (const advice of resolutionAdvice(c)) {
      lines.push(`    * ${advice}`);
    }
    lines.push("");
  }
  return lines.join("\n");
}
