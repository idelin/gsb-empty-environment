/**
 * Min-cost syntax repairer.
 *
 * Model: a left-to-right walk over the input tokens. At each gap (before
 * token i, and at the end) we may insert tokens; each input token may be
 * kept (cost 0, must fit the grammar) or deleted (cost 1). Each insertion
 * costs 1. The repair script is the sequence of edits in canonical walk
 * order: all insertions at a gap come before the keep/delete decision on
 * the token that follows the gap.
 *
 * Search: Dijkstra over configurations (input index, parser stack,
 * justDeleted flag), prioritized by (cost, edit script) where scripts are
 * ordered lexicographically via `compareEditScripts`. Because every edit
 * costs exactly 1, all competing scripts for a configuration have equal
 * length, which makes lexicographic Dijkstra sound: the first ACCEPT
 * configuration popped is globally optimal in (cost, script) order. In
 * particular we never stop at the first parseable script we happen to
 * generate -- only at the first one popped from the priority queue.
 *
 * Pruning (both sound):
 *  - an upper bound from a greedy repair (and the trivial "delete all,
 *    insert one INT" repair), so configurations that cannot beat it are
 *    never pushed;
 *  - an admissible lower bound from parenthesis/bracket balance: every
 *    unmatched closer already on the parser stack must eventually be
 *    matched, and the remaining input can supply at most its own closers.
 *
 * Budget: `budget` bounds the number of expanded configurations. When it
 * is exceeded we report only the current bounds:
 *   - lower bound = min over queued configurations of cost + lowerBound
 *     (every repair still to be discovered passes through the queue);
 *   - upper bound = the greedy/trivial repair cost.
 */

import { parseAst, reduceStack, type Ast, type GrammarSymbol } from "./grammar.ts";
import { INSERT_TEXT, INSERTABLE, tokenize, type Token, type TokenType } from "./tokens.ts";

export type Edit =
  | { kind: "insert"; gap: number; token: TokenType }
  | { kind: "delete"; index: number };

function editPosition(edit: Edit): number {
  return edit.kind === "insert" ? edit.gap : edit.index;
}

/** Insertions sort before deletions at the same position (walk order). */
function editKindRank(edit: Edit): number {
  return edit.kind === "insert" ? 0 : 1;
}

export function compareEdits(a: Edit, b: Edit): number {
  const posDiff = editPosition(a) - editPosition(b);
  if (posDiff !== 0) return posDiff;
  const kindDiff = editKindRank(a) - editKindRank(b);
  if (kindDiff !== 0) return kindDiff;
  if (a.kind === "insert" && b.kind === "insert") {
    const ta = INSERT_TEXT[a.token];
    const tb = INSERT_TEXT[b.token];
    if (ta !== tb) return ta < tb ? -1 : 1;
  }
  return 0;
}

/** Lexicographic comparison of two edit scripts. */
export function compareEditScripts(a: readonly Edit[], b: readonly Edit[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    const c = compareEdits(a[i], b[i]);
    if (c !== 0) return c;
  }
  return a.length - b.length;
}

interface Config {
  i: number;
  stack: GrammarSymbol[];
  /** True right after a deletion; blocks insertions so that each physical
   *  repair has exactly one canonical script (inserts precede deletes). */
  justDeleted: boolean;
  cost: number;
  /** Admissible lower bound on further edits needed from this config. */
  lb: number;
  edits: Edit[];
}

function configKey(c: Config): string {
  return `${c.i}|${c.justDeleted ? 1 : 0}|${c.stack.join(",")}`;
}

class MinHeap {
  private items: Config[] = [];
  size(): number {
    return this.items.length;
  }
  minCostPlusLb(): number {
    let best = Infinity;
    for (const item of this.items) best = Math.min(best, item.cost + item.lb);
    return best;
  }
  private static less(a: Config, b: Config): boolean {
    if (a.cost !== b.cost) return a.cost < b.cost;
    return compareEditScripts(a.edits, b.edits) < 0;
  }
  push(item: Config): void {
    const items = this.items;
    items.push(item);
    let i = items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (!MinHeap.less(items[i], items[parent])) break;
      [items[i], items[parent]] = [items[parent], items[i]];
      i = parent;
    }
  }
  pop(): Config {
    const items = this.items;
    const top = items[0];
    const last = items.pop()!;
    if (items.length > 0) {
      items[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let smallest = i;
        if (l < items.length && MinHeap.less(items[l], items[smallest])) smallest = l;
        if (r < items.length && MinHeap.less(items[r], items[smallest])) smallest = r;
        if (smallest === i) break;
        [items[i], items[smallest]] = [items[smallest], items[i]];
        i = smallest;
      }
    }
    return top;
  }
}

export interface RepairOk {
  status: "ok";
  cost: number;
  edits: Edit[];
  /** Parse tree of the repaired token stream. */
  ast: Ast;
  /** Token stream after applying the edits; kept tokens retain their
   *  original `pos`, inserted tokens are anchored at the insertion point. */
  repairedTokens: Token[];
  expansions: number;
}

export interface RepairBudgetExceeded {
  status: "budget-exceeded";
  lowerBound: number;
  upperBound: number;
  expansions: number;
}

export type RepairResult = RepairOk | RepairBudgetExceeded;

export interface RepairOptions {
  /** Maximum number of search configurations to expand. */
  budget?: number;
}

export const DEFAULT_BUDGET = 100_000;

/** Cost of a valid repair found greedily; never exceeds `trivial`. */
function greedyRepairCost(tokens: readonly Token[], trivial: number, preferInsert: boolean): number {
  let stack: GrammarSymbol[] = ["S"];
  let cost = 0;
  let i = 0;
  const n = tokens.length;
  const acceptedInsert = (s: GrammarSymbol[]): TokenType | null => {
    for (const t of INSERTABLE) {
      const r = reduceStack(s, t);
      if (r !== null && r[r.length - 1] === t) return t;
    }
    return null;
  };
  while (cost <= trivial) {
    if (i === n) {
      const atEof = reduceStack(stack, "EOF");
      if (atEof !== null && atEof.length === 1 && atEof[0] === "EOF") return cost;
      if (atEof !== null && atEof.length > 0) {
        const top = atEof[atEof.length - 1];
        if (top === "RPAREN" || top === "RBRACKET" || top === "COMMA" ||
            top === "PLUS" || top === "MINUS" || top === "STAR" || top === "SLASH" ||
            top === "INT" || top === "IDENT" || top === "LPAREN" || top === "LBRACKET") {
          atEof.pop();
          stack = atEof;
          cost++;
          continue;
        }
      }
      const t = acceptedInsert(stack);
      if (t === null) return trivial;
      const r = reduceStack(stack, t)!;
      r.pop();
      stack = r;
      cost++;
      continue;
    }
    const actual = tokens[i].type;
    const kept = reduceStack(stack, actual);
    const canKeep = kept !== null && kept[kept.length - 1] === actual;
    if (canKeep && !preferInsert) {
      kept!.pop();
      stack = kept!;
      i++;
      continue;
    }
    const t = acceptedInsert(stack);
    if (t !== null && (preferInsert || !canKeep)) {
      const r = reduceStack(stack, t)!;
      r.pop();
      stack = r;
      cost++;
      continue;
    }
    if (canKeep) {
      kept!.pop();
      stack = kept!;
      i++;
      continue;
    }
    i++;
    cost++;
  }
  return trivial;
}

export function repairTokens(tokens: readonly Token[], options: RepairOptions = {}): RepairResult {
  const n = tokens.length;
  const budget = options.budget ?? DEFAULT_BUDGET;
  const trivial = n + 1; // delete all tokens, then insert one INT
  const upperBound = Math.min(
    trivial,
    greedyRepairCost(tokens, trivial, true),
    greedyRepairCost(tokens, trivial, false),
  );

  // Suffix counts of closers in the input, for the balance lower bound.
  const remRP = new Array<number>(n + 1).fill(0);
  const remRB = new Array<number>(n + 1).fill(0);
  for (let i = n - 1; i >= 0; i--) {
    remRP[i] = remRP[i + 1] + (tokens[i].type === "RPAREN" ? 1 : 0);
    remRB[i] = remRB[i + 1] + (tokens[i].type === "RBRACKET" ? 1 : 0);
  }
  const balanceLb = (stack: readonly GrammarSymbol[], i: number): number => {
    let pendingP = 0;
    let pendingB = 0;
    for (const sym of stack) {
      if (sym === "RPAREN") pendingP++;
      else if (sym === "RBRACKET") pendingB++;
    }
    return Math.max(0, pendingP - remRP[i]) + Math.max(0, pendingB - remRB[i]);
  };

  const heap = new MinHeap();
  const settled = new Set<string>();
  let expansions = 0;
  const tryPush = (config: Config): void => {
    if (config.cost + config.lb > upperBound) return;
    heap.push(config);
  };

  tryPush({ i: 0, stack: ["S"], justDeleted: false, cost: 0, lb: balanceLb(["S"], 0), edits: [] });

  while (heap.size() > 0) {
    if (expansions >= budget) {
      return {
        status: "budget-exceeded",
        lowerBound: Math.min(heap.minCostPlusLb(), upperBound),
        upperBound,
        expansions,
      };
    }
    const config = heap.pop();
    const key = configKey(config);
    if (settled.has(key)) continue;
    settled.add(key);
    expansions++;

    // Accept: whole input consumed and the parser expects exactly EOF.
    if (config.i === n) {
      const reduced = reduceStack(config.stack, "EOF");
      if (reduced !== null && reduced.length === 1 && reduced[0] === "EOF") {
        const repairedTokens = applyEdits(tokens, config.edits);
        return {
          status: "ok",
          cost: config.cost,
          edits: config.edits,
          ast: parseAst(repairedTokens)!,
          repairedTokens,
          expansions,
        };
      }
    }

    if (config.i < n) {
      const actual = tokens[config.i].type;
      // Keep (cost 0): only if the parser can consume this exact token.
      const kept = reduceStack(config.stack, actual);
      if (kept !== null && kept[kept.length - 1] === actual) {
        kept.pop();
        tryPush({
          i: config.i + 1,
          stack: kept,
          justDeleted: false,
          cost: config.cost,
          lb: balanceLb(kept, config.i + 1),
          edits: config.edits,
        });
      }
      // Delete (cost 1).
      tryPush({
        i: config.i + 1,
        stack: config.stack,
        justDeleted: true,
        cost: config.cost + 1,
        lb: balanceLb(config.stack, config.i + 1),
        edits: [...config.edits, { kind: "delete", index: config.i }],
      });
    }

    // Insert (cost 1): any terminal the parser can consume right now.
    if (!config.justDeleted) {
      for (const inserted of INSERTABLE) {
        const reduced = reduceStack(config.stack, inserted);
        if (reduced !== null && reduced[reduced.length - 1] === inserted) {
          reduced.pop();
          tryPush({
            i: config.i,
            stack: reduced,
            justDeleted: false,
            cost: config.cost + 1,
            lb: balanceLb(reduced, config.i),
            edits: [...config.edits, { kind: "insert", gap: config.i, token: inserted }],
          });
        }
      }
    }
  }

  // Unreachable in practice: the trivial repair (insert INT, delete all)
  // always fits within the upper bound. Reported as tight bounds anyway.
  return { status: "budget-exceeded", lowerBound: upperBound, upperBound, expansions };
}

export function repair(source: string, options: RepairOptions = {}): RepairResult {
  return repairTokens(tokenize(source), options);
}

/** Applies a canonical edit script, preserving positions of kept tokens. */
export function applyEdits(tokens: readonly Token[], edits: readonly Edit[]): Token[] {
  const insertsAt = new Map<number, Edit[]>();
  const deleted = new Set<number>();
  for (const edit of edits) {
    if (edit.kind === "insert") {
      const list = insertsAt.get(edit.gap);
      if (list === undefined) insertsAt.set(edit.gap, [edit]);
      else list.push(edit);
    } else {
      deleted.add(edit.index);
    }
  }
  const endPos = tokens.length === 0
    ? 0
    : tokens[tokens.length - 1].pos + tokens[tokens.length - 1].text.length;
  const out: Token[] = [];
  for (let i = 0; i <= tokens.length; i++) {
    for (const edit of insertsAt.get(i) ?? []) {
      if (edit.kind === "insert") {
        out.push({
          type: edit.token,
          text: INSERT_TEXT[edit.token],
          pos: i < tokens.length ? tokens[i].pos : endPos,
        });
      }
    }
    if (i < tokens.length && !deleted.has(i)) out.push(tokens[i]);
  }
  return out;
}

/** Normalized source rendering of a token stream (single-space separated). */
export function tokensToSource(tokens: readonly Token[]): string {
  return tokens.map((t) => t.text).join(" ");
}

/** Human-readable edit descriptions that reference original positions. */
export function formatEdits(tokens: readonly Token[], edits: readonly Edit[]): string[] {
  return edits.map((edit) => {
    if (edit.kind === "delete") {
      const tok = tokens[edit.index];
      return `delete token ${edit.index} ('${tok.text}' at pos ${tok.pos})`;
    }
    const anchor = edit.gap < tokens.length
      ? `before token ${edit.gap} ('${tokens[edit.gap].text}' at pos ${tokens[edit.gap].pos})`
      : "at end of input";
    return `insert '${INSERT_TEXT[edit.token]}' ${anchor}`;
  });
}
