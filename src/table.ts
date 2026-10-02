/**
 * ACTION/GOTO 分析表生成与 Shift/Reduce、Reduce/Reduce 冲突检测。
 */

import { END } from "./grammar.ts";
import type { Automaton, Item } from "./lr1.ts";

export type Action =
  | { kind: "shift"; state: number }
  | { kind: "reduce"; prod: number }
  | { kind: "accept" };

export function actionEquals(a: Action, b: Action): boolean {
  if (a.kind !== b.kind) return false;
  if (a.kind === "shift" && b.kind === "shift") return a.state === b.state;
  if (a.kind === "reduce" && b.kind === "reduce") return a.prod === b.prod;
  return true;
}

export function formatAction(a: Action, prodText?: (prod: number) => string): string {
  switch (a.kind) {
    case "shift":
      return `shift ${a.state}`;
    case "reduce":
      return `reduce ${prodText ? prodText(a.prod) : `#${a.prod}`}`;
    case "accept":
      return "accept";
  }
}

export type ConflictType = "shift/reduce" | "reduce/reduce";

export interface Conflict {
  type: ConflictType;
  state: number;
  /** 触发冲突的输入 token（终结符或 $） */
  token: string;
  /** 该单元格中相互竞争的全部动作 */
  actions: Action[];
  /** 引发冲突的 LR(1) 项（移进项与归约项） */
  items: Item[];
}

export interface ParseTables {
  /** state -> token -> 动作集合（正常为 1 个，冲突时 >1 个） */
  action: Map<number, Map<string, Action[]>>;
  /** state -> 非终结符 -> 目标状态 */
  goto: Map<number, Map<string, number>>;
  conflicts: Conflict[];
}

function addAction(cell: Action[], a: Action): void {
  if (!cell.some((x) => actionEquals(x, a))) cell.push(a);
}

/** 由规范 LR(1) 自动机构建 ACTION/GOTO 表，并检测冲突。 */
export function buildTables(automaton: Automaton): ParseTables {
  const { grammar: g, states } = automaton;
  const action = new Map<number, Map<string, Action[]>>();
  const gotoTable = new Map<number, Map<string, number>>();

  const cellAt = (state: number, token: string): Action[] => {
    let row = action.get(state);
    if (!row) {
      row = new Map();
      action.set(state, row);
    }
    let cell = row.get(token);
    if (!cell) {
      cell = [];
      row.set(token, cell);
    }
    return cell;
  };

  for (const state of states) {
    // GOTO 表：非终结符转移
    for (const [sym, target] of state.transitions) {
      if (g.nonterminals.has(sym)) {
        let row = gotoTable.get(state.id);
        if (!row) {
          row = new Map();
          gotoTable.set(state.id, row);
        }
        row.set(sym, target);
      }
    }
    // ACTION 表
    for (const item of state.items) {
      const prod = g.productions[item.prod];
      if (item.dot < prod.rhs.length) {
        const sym = prod.rhs[item.dot];
        if (g.terminals.has(sym)) {
          const target = state.transitions.get(sym);
          if (target !== undefined) {
            addAction(cellAt(state.id, sym), { kind: "shift", state: target });
          }
        }
      } else if (item.prod === 0 && item.lookahead === END) {
        addAction(cellAt(state.id, END), { kind: "accept" });
      } else {
        addAction(cellAt(state.id, item.lookahead), { kind: "reduce", prod: item.prod });
      }
    }
  }

  // 冲突检测与分类
  const conflicts: Conflict[] = [];
  for (const [stateId, row] of action) {
    for (const [token, actions] of row) {
      if (actions.length <= 1) continue;
      const hasShift = actions.some((a) => a.kind === "shift");
      const reduceCount = actions.filter((a) => a.kind === "reduce").length;
      const type: ConflictType =
        hasShift && reduceCount >= 1
          ? "shift/reduce"
          : reduceCount >= 2
            ? "reduce/reduce"
            : (() => {
                throw new Error(`未知的冲突形态: state ${stateId} token ${token}`);
              })();
      conflicts.push({
        type,
        state: stateId,
        token,
        actions,
        items: conflictItems(automaton, stateId, token),
      });
    }
  }
  conflicts.sort((a, b) => a.state - b.state || (a.token < b.token ? -1 : 1));
  return { action, goto: gotoTable, conflicts };
}

/** 收集状态 state 中在 token 上引发冲突的 LR(1) 项。 */
function conflictItems(automaton: Automaton, stateId: number, token: string): Item[] {
  const { grammar: g, states } = automaton;
  const result: Item[] = [];
  for (const item of states[stateId].items) {
    const prod = g.productions[item.prod];
    const isShiftItem = item.dot < prod.rhs.length && prod.rhs[item.dot] === token;
    const isReduceItem = item.dot === prod.rhs.length && item.lookahead === token;
    if (isShiftItem || isReduceItem) result.push(item);
  }
  return result;
}

export type ResolvePolicy = "shift" | "reduce";

/**
 * 按策略消解冲突，返回每个单元格只含单一动作的表。
 * - "shift"：Shift/Reduce 冲突优先移进（Yacc/Bison 默认行为）；
 * - "reduce"：优先按编号最小的产生式归约。
 * Reduce/Reduce 冲突在两种策略下都取编号最小的产生式（仅用于继续分析，不代表语义正确）。
 */
export function resolveConflicts(tables: ParseTables, policy: ResolvePolicy): ParseTables {
  const action = new Map<number, Map<string, Action[]>>();
  for (const [stateId, row] of tables.action) {
    const newRow = new Map<string, Action[]>();
    for (const [token, actions] of row) {
      if (actions.length <= 1) {
        newRow.set(token, actions);
        continue;
      }
      const shift = actions.find((a) => a.kind === "shift");
      const reduces = actions
        .filter((a): a is { kind: "reduce"; prod: number } => a.kind === "reduce")
        .sort((a, b) => a.prod - b.prod);
      const chosen =
        policy === "shift" && shift ? shift : reduces[0] ?? shift ?? actions[0];
      newRow.set(token, [chosen]);
    }
    action.set(stateId, newRow);
  }
  return { action, goto: tables.goto, conflicts: tables.conflicts };
}
