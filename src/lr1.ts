import {
  END,
  EPSILON,
  computeFirst,
  firstOfSequence,
} from "./grammar.ts";
import type { Grammar } from "./grammar.ts";

/** An LR(1) item: production `prod` with the dot at `dot` and lookahead `la`. */
export interface Item {
  prod: number;
  dot: number;
  la: string;
}

export type Action =
  | { type: "shift"; to: number }
  | { type: "reduce"; prod: number }
  | { type: "accept" };

export interface Conflict {
  state: number;
  token: string;
  kind: "shift/reduce" | "reduce/reduce";
  actions: Action[];
}

export interface LR1Table {
  grammar: Grammar;
  states: Item[][];
  transitions: Array<Map<string, number>>;
  action: Array<Map<string, Action[]>>;
  gotoTable: Array<Map<string, number>>;
  conflicts: Conflict[];
}

const itemKey = (it: Item): string => `${it.prod}|${it.dot}|${it.la}`;

const stateKey = (items: Item[]): string =>
  items
    .map(itemKey)
    .sort()
    .join(";");

function formatProd(g: Grammar, prodIdx: number): string {
  const p = g.productions[prodIdx];
  return `${p.lhs} -> ${p.rhs.length === 0 ? EPSILON : p.rhs.join(" ")}`;
}

export function formatItem(g: Grammar, it: Item): string {
  const p = g.productions[it.prod];
  const rhs = [...p.rhs.slice(0, it.dot), "•", ...p.rhs.slice(it.dot)];
  return `[${p.lhs} -> ${rhs.join(" ")}, ${it.la}]`;
}

export function formatAction(g: Grammar, a: Action): string {
  switch (a.type) {
    case "shift":
      return `shift -> state ${a.to}`;
    case "reduce":
      return `reduce by ${formatProd(g, a.prod)}`;
    case "accept":
      return "accept";
  }
}

/** LR(1) closure: for [A -> α•Bβ, a] add [B -> •γ, b] for every b ∈ FIRST(βa). */
export function closure(
  g: Grammar,
  first: Map<string, Set<string>>,
  items: Item[],
): Item[] {
  const result = new Map<string, Item>();
  const worklist: Item[] = [];
  for (const it of items) {
    result.set(itemKey(it), it);
    worklist.push(it);
  }
  while (worklist.length > 0) {
    const it = worklist.pop()!;
    const p = g.productions[it.prod];
    if (it.dot >= p.rhs.length) continue;
    const B = p.rhs[it.dot];
    if (!g.nonTerminals.has(B)) continue;
    const beta = p.rhs.slice(it.dot + 1);
    const lookaheads = firstOfSequence(g, first, beta, [it.la]);
    for (let idx = 0; idx < g.productions.length; idx++) {
      if (g.productions[idx].lhs !== B) continue;
      for (const b of lookaheads) {
        const next: Item = { prod: idx, dot: 0, la: b };
        const key = itemKey(next);
        if (!result.has(key)) {
          result.set(key, next);
          worklist.push(next);
        }
      }
    }
  }
  return [...result.values()];
}

export function gotoSet(
  g: Grammar,
  first: Map<string, Set<string>>,
  items: Item[],
  symbol: string,
): Item[] {
  const moved: Item[] = [];
  for (const it of items) {
    const p = g.productions[it.prod];
    if (it.dot < p.rhs.length && p.rhs[it.dot] === symbol) {
      moved.push({ prod: it.prod, dot: it.dot + 1, la: it.la });
    }
  }
  return moved.length === 0 ? [] : closure(g, first, moved);
}

/** Build the canonical LR(1) collection plus ACTION/GOTO tables and conflicts. */
export function buildLR1Table(source: Grammar): LR1Table {
  const augStart = `${source.start}'`;
  const grammar: Grammar = {
    start: augStart,
    terminals: new Set(source.terminals),
    nonTerminals: new Set([...source.nonTerminals, augStart]),
    productions: [
      { lhs: augStart, rhs: [source.start] },
      ...source.productions,
    ],
  };
  const first = computeFirst(grammar);

  const states: Item[][] = [];
  const transitions: Array<Map<string, number>> = [];
  const seen = new Map<string, number>();

  const startItems = closure(grammar, first, [
    { prod: 0, dot: 0, la: END },
  ]);
  states.push(startItems);
  transitions.push(new Map());
  seen.set(stateKey(startItems), 0);

  for (let i = 0; i < states.length; i++) {
    const symbols = new Set<string>();
    for (const it of states[i]) {
      const p = grammar.productions[it.prod];
      if (it.dot < p.rhs.length) {
        symbols.add(p.rhs[it.dot]);
      }
    }
    for (const sym of symbols) {
      const target = gotoSet(grammar, first, states[i], sym);
      if (target.length === 0) continue;
      const key = stateKey(target);
      let id = seen.get(key);
      if (id === undefined) {
        id = states.length;
        seen.set(key, id);
        states.push(target);
        transitions.push(new Map());
      }
      transitions[i].set(sym, id);
    }
  }

  const action: Array<Map<string, Action[]>> = states.map(() => new Map());
  const gotoTable: Array<Map<string, number>> = states.map(() => new Map());

  const addAction = (state: number, token: string, act: Action): void => {
    const row = action[state];
    if (!row.has(token)) row.set(token, []);
    const cell = row.get(token)!;
    if (!cell.some((a) => formatAction(grammar, a) === formatAction(grammar, act))) {
      cell.push(act);
    }
  };

  for (let i = 0; i < states.length; i++) {
    for (const [sym, target] of transitions[i]) {
      if (grammar.terminals.has(sym)) {
        addAction(i, sym, { type: "shift", to: target });
      } else {
        gotoTable[i].set(sym, target);
      }
    }
    for (const it of states[i]) {
      const p = grammar.productions[it.prod];
      if (it.dot !== p.rhs.length) continue;
      if (it.prod === 0 && it.la === END) {
        addAction(i, END, { type: "accept" });
      } else {
        addAction(i, it.la, { type: "reduce", prod: it.prod });
      }
    }
  }

  const conflicts: Conflict[] = [];
  for (let i = 0; i < states.length; i++) {
    for (const [token, acts] of action[i]) {
      if (acts.length < 2) continue;
      const hasShift = acts.some((a) => a.type === "shift");
      const reduceCount = acts.filter((a) => a.type === "reduce").length;
      let kind: Conflict["kind"];
      if (hasShift && reduceCount > 0) kind = "shift/reduce";
      else kind = "reduce/reduce";
      conflicts.push({ state: i, token, kind, actions: acts });
    }
  }

  return { grammar, states, transitions, action, gotoTable, conflicts };
}
