/**
 * 冲突诊断：最短输入 token 串证据生成、证据模拟验证与解决建议。
 *
 * 证据生成原理：冲突发生在状态 I、向前看符号为 a 时，
 * 即存在一段输入 w·a，使解析器读完 w 后处于状态 I。
 * 在自动机上从初始状态到 I 的每条路径对应一个可行前缀（viable prefix），
 * 路径上终结符边消耗 1 个 token，非终结符边 X 消耗 minYield(X) 个 token
 * （X 能推导出的最短终结符串）。用 Dijkstra 求 token 数最少的路径，
 * 再附上冲突 token a，即为最短输入证据。
 */

import { END, computeMinYield, formatProduction, type Grammar } from "./grammar.ts";
import type { Automaton } from "./lr1.ts";
import { formatAction, type Action, type Conflict, type ParseTables } from "./table.ts";

export interface ConflictDiagnosis {
  conflict: Conflict;
  /** 最短输入 token 串证据（含末尾的冲突 token） */
  evidence: string[];
  /** 证据是否通过非确定性模拟验证 */
  verified: boolean;
  /** 解决建议 */
  suggestions: string[];
}

/**
 * 在自动机上用 Dijkstra 求从初始状态到 targetState 的最短输入 token 序列，
 * 末尾追加冲突 token。
 */
export function shortestEvidence(
  automaton: Automaton,
  minYield: Map<string, string[]>,
  targetState: number,
  conflictToken: string,
): string[] | null {
  const n = automaton.states.length;
  const dist = new Array<number>(n).fill(Infinity);
  const prev = new Array<{ from: number; symbol: string } | null>(n).fill(null);
  dist[0] = 0;
  const done = new Array<boolean>(n).fill(false);

  for (;;) {
    let u = -1;
    for (let i = 0; i < n; i++) {
      if (!done[i] && dist[i] < (u === -1 ? Infinity : dist[u])) u = i;
    }
    if (u === -1) break;
    done[u] = true;
    if (u === targetState) break;
    for (const [sym, v] of automaton.states[u].transitions) {
      const y = minYield.get(sym);
      if (!y) continue; // 该符号无法推出终结符串，边不可用
      const w = y.length;
      if (dist[u] + w < dist[v]) {
        dist[v] = dist[u] + w;
        prev[v] = { from: u, symbol: sym };
      }
    }
  }

  if (!done[targetState] && targetState !== 0) return null;
  const tokens: string[] = [];
  let cur = targetState;
  const chain: string[] = [];
  while (cur !== 0) {
    const p = prev[cur];
    if (!p) return null;
    chain.push(p.symbol);
    cur = p.from;
  }
  chain.reverse();
  for (const sym of chain) tokens.push(...minYield.get(sym)!);
  // 冲突 token 是真实输入符号时附在末尾；$ 表示输入结束，不计入输入串
  if (conflictToken !== END) tokens.push(conflictToken);
  return tokens;
}

/**
 * 非确定性 LR 模拟：在冲突单元格尝试所有动作，
 * 验证输入 evidence 确实能让某个解析轨迹在冲突 token 处到达冲突状态。
 */
export function verifyEvidence(
  automaton: Automaton,
  tables: ParseTables,
  conflict: Conflict,
  evidence: string[],
): boolean {
  const g = automaton.grammar;
  interface Config {
    stack: number[]; // 状态栈
    pos: number; // 已消耗的输入 token 数
  }
  const start: Config = { stack: [0], pos: 0 };
  const queue: Config[] = [start];
  const visited = new Set<string>([`0@0`]);
  let expansions = 0;
  const MAX_EXPANSIONS = 200_000;

  while (queue.length > 0 && expansions < MAX_EXPANSIONS) {
    const { stack, pos } = queue.shift()!;
    expansions++;
    const top = stack[stack.length - 1];
    const lookahead = pos < evidence.length ? evidence[pos] : END;
    if (top === conflict.state && lookahead === conflict.token) return true;
    const actions = tables.action.get(top)?.get(lookahead) ?? [];
    for (const action of actions) {
      let next: Config | null = null;
      if (action.kind === "shift") {
        if (pos >= evidence.length) continue;
        next = { stack: [...stack, action.state], pos: pos + 1 };
      } else if (action.kind === "reduce") {
        const prod = g.productions[action.prod];
        if (stack.length - 1 < prod.rhs.length) continue;
        const newStack = stack.slice(0, stack.length - prod.rhs.length);
        const gotoTarget = tables.goto.get(newStack[newStack.length - 1])?.get(prod.lhs);
        if (gotoTarget === undefined) continue;
        next = { stack: [...newStack, gotoTarget], pos };
      }
      if (next) {
        const key = `${next.stack.join(",")}@${next.pos}`;
        if (!visited.has(key)) {
          visited.add(key);
          queue.push(next);
        }
      }
    }
  }
  return false;
}

/** 针对具体冲突生成解决建议。 */
export function suggestResolutions(conflict: Conflict, g: Grammar): string[] {
  const prodText = (id: number) => formatProduction(g, id);
  if (conflict.type === "shift/reduce") {
    const reduceProds = conflict.actions
      .filter((a): a is { kind: "reduce"; prod: number } => a.kind === "reduce")
      .map((a) => prodText(a.prod));
    return [
      `采用 SHIFT 优先策略（Yacc/Bison 的默认行为）：遇到 ${conflict.token} 时选择移进，` +
        `使其绑定到最近（内层）的结构；本工具可通过 resolveConflicts(tables, "shift") 生成消解后的表。`,
      `若语义要求 ${conflict.token} 归属外层结构，则选择 REDUCE 优先（resolveConflicts(tables, "reduce")），` +
        `即按产生式 ${reduceProds.join(" / ")} 归约。`,
      `为 token 与产生式声明优先级/结合性（如 %prec、%left/%right/%nonassoc），让表生成器自动消解。`,
      `重写文法消除二义性：将结构按“已匹配/未匹配”拆分` +
        `（例如 dangling-else 可拆为 matched / unmatched 两类语句，使 else 只能与最近的未匹配 if 结合）。`,
    ];
  }
  const prods = conflict.actions
    .filter((a): a is { kind: "reduce"; prod: number } => a.kind === "reduce")
    .map((a) => prodText(a.prod));
  return [
    `Reduce/Reduce 冲突无法用优先级/结合性消解：在状态 ${conflict.state} 遇到 ${conflict.token} 时，` +
      `产生式 ${prods.join(" 与 ")} 都可归约，文法对该输入存在真实的二义性。`,
    `重构文法：合并或区分上述产生式的上下文（如引入新的非终结符、调整符号的使用位置），` +
      `使同一状态下至多只有一条可归约的产生式。`,
    `检查是否有两条产生式实质重复，或某个非终结符的用途可以被合并。`,
  ];
}

/** 对全部冲突生成完整诊断。 */
export function diagnoseConflicts(
  automaton: Automaton,
  tables: ParseTables,
): ConflictDiagnosis[] {
  const minYield = computeMinYield(automaton.sourceGrammar);
  return tables.conflicts.map((conflict) => {
    const evidence =
      shortestEvidence(automaton, minYield, conflict.state, conflict.token) ?? [];
    const verified =
      evidence.length > 0 && verifyEvidence(automaton, tables, conflict, evidence);
    return {
      conflict,
      evidence,
      verified,
      suggestions: suggestResolutions(conflict, automaton.grammar),
    };
  });
}

/** 供报告使用的动作描述（含引发冲突的项）。 */
export function describeActions(
  conflict: Conflict,
  g: Grammar,
): { action: Action; text: string }[] {
  const prodText = (id: number) => formatProduction(g, id);
  return conflict.actions.map((action) => ({
    action,
    text: formatAction(action, prodText),
  }));
}
