/**
 * Independent brute-force repairer used to cross-check `repairTokens`.
 *
 * Unlike the main algorithm it does NOT simulate the parser during the
 * search. It enumerates raw edit scripts over the token stream (materializing
 * the resulting token-type string) with the same (cost, script) priority and
 * the same canonical walk order, and only checks grammaticality when a
 * candidate output is complete. If the main search ever mistook an early
 * parseable script for the optimum, this enumerator finds a cheaper or
 * lexicographically smaller one.
 *
 * `costCap` prunes scripts costlier than the cap. Passing the main
 * algorithm's reported cost keeps the enumeration small while remaining a
 * sound optimality check: any cheaper or same-cost-but-smaller script the
 * main search could have missed still lies inside the enumerated space.
 *
 * Pruning (both sound, both independent of the main algorithm):
 *  - configs already settled are never re-pushed (Dijkstra label-setting);
 *  - an admissible lower bound from parenthesis/bracket balance: output
 *    tokens already emitted cannot be un-emitted, so a config whose net
 *    opens cannot be closed by the remaining input needs at least that
 *    many further insertions.
 *
 * Implementation notes: the (cost, script) order is realized here with a
 * fixed-width string encoding of the script (independent of `compareEdits`
 * in repair.ts), and the partial output is packed into a number behind a
 * sentinel bit so the length is part of the encoding.
 */

import { parses } from "./grammar.ts";
import { INSERT_TEXT, INSERTABLE, type Token, type TokenType } from "./tokens.ts";
import type { Edit } from "./repair.ts";

/** Insertable tokens ordered by their canonical text (must match the
 *  lexicographic order `compareEdits` induces on inserted tokens). */
const TOKEN_ORDER: TokenType[] = [...INSERTABLE].sort((a, b) =>
  INSERT_TEXT[a] < INSERT_TEXT[b] ? -1 : 1,
);
const INSERT_RANK = new Map<TokenType, number>(TOKEN_ORDER.map((t, i) => [t, i]));

/** All token types that may appear in an enumerated output string. */
const OUT_TYPES: TokenType[] = [...INSERTABLE, "INVALID"];
const OUT_INDEX = new Map<TokenType, number>(OUT_TYPES.map((t, i) => [t, i]));

// 4 bits per output token behind a sentinel, plus 5 settled-key bits for
// (i, justDeleted), stays below 2^53 so numeric keys are exact.
const MAX_OUT_LEN = 11;

/** Encodes one edit as two chars: position, then kind/token. Inserts rank
 *  0..10 by token text; a delete ranks 15, so inserts sort before deletes
 *  at the same position, matching `compareEdits`. */
function encodeEdit(edit: Edit): string {
  if (edit.kind === "insert") {
    return String.fromCharCode(edit.gap, INSERT_RANK.get(edit.token)!);
  }
  return String.fromCharCode(edit.index, 15);
}

interface BfConfig {
  i: number;
  outNum: number; // packed output token indices, 4 bits each, sentinel-led
  outLen: number;
  openP: number;
  closeP: number;
  openB: number;
  closeB: number;
  justDeleted: boolean;
  cost: number;
  key: string; // encoded script, ordered like compareEditScripts
  edits: Edit[];
}

export interface BruteForceResult {
  cost: number;
  edits: Edit[];
  output: TokenType[];
  expansions: number;
}

export interface BruteForceOptions {
  costCap?: number;
  expansionCap?: number;
}

const DELTA: Partial<Record<TokenType, [number, number, number, number]>> = {
  LPAREN: [1, 0, 0, 0],
  RPAREN: [0, 1, 0, 0],
  LBRACKET: [0, 0, 1, 0],
  RBRACKET: [0, 0, 0, 1],
};

export function bruteForceRepair(
  tokens: readonly Token[],
  options: BruteForceOptions = {},
): BruteForceResult | null {
  const n = tokens.length;
  const costCap = options.costCap ?? n + 1;
  const expansionCap = options.expansionCap ?? 2_000_000;
  if (n + costCap > MAX_OUT_LEN) {
    throw new Error(`brute force supports at most ${MAX_OUT_LEN} output tokens`);
  }

  // Suffix counts of the remaining input, for the balance lower bound.
  const remLP = new Array<number>(n + 1).fill(0);
  const remRP = new Array<number>(n + 1).fill(0);
  const remLB = new Array<number>(n + 1).fill(0);
  const remRB = new Array<number>(n + 1).fill(0);
  for (let i = n - 1; i >= 0; i--) {
    remLP[i] = remLP[i + 1] + (tokens[i].type === "LPAREN" ? 1 : 0);
    remRP[i] = remRP[i + 1] + (tokens[i].type === "RPAREN" ? 1 : 0);
    remLB[i] = remLB[i + 1] + (tokens[i].type === "LBRACKET" ? 1 : 0);
    remRB[i] = remRB[i + 1] + (tokens[i].type === "RBRACKET" ? 1 : 0);
  }

  const heap: BfConfig[] = [];
  const less = (a: BfConfig, b: BfConfig): boolean =>
    a.cost !== b.cost ? a.cost < b.cost : a.key < b.key;
  const settled = new Set<number>();
  const settledKeyOf = (c: BfConfig): number =>
    c.outNum * 32 + c.i * 2 + (c.justDeleted ? 1 : 0);
  const push = (c: BfConfig): void => {
    heap.push(c);
    let i = heap.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (!less(heap[i], heap[p])) break;
      [heap[i], heap[p]] = [heap[p], heap[i]];
      i = p;
    }
  };
  const pop = (): BfConfig => {
    const top = heap[0];
    const last = heap.pop()!;
    if (heap.length > 0) {
      heap[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let s = i;
        if (l < heap.length && less(heap[l], heap[s])) s = l;
        if (r < heap.length && less(heap[r], heap[s])) s = r;
        if (s === i) break;
        [heap[i], heap[s]] = [heap[s], heap[i]];
        i = s;
      }
    }
    return top;
  };
  const tryPush = (c: BfConfig): void => {
    const netP = c.openP - c.closeP;
    const netB = c.openB - c.closeB;
    const lowerBound =
      Math.max(0, netP - remRP[c.i]) +
      Math.max(0, -netP - remLP[c.i]) +
      Math.max(0, netB - remRB[c.i]) +
      Math.max(0, -netB - remLB[c.i]);
    if (c.cost + lowerBound > costCap) return;
    if (settled.has(settledKeyOf(c))) return;
    push(c);
  };

  let expansions = 0;
  push({ i: 0, outNum: 1, outLen: 0, openP: 0, closeP: 0, openB: 0, closeB: 0, justDeleted: false, cost: 0, key: "", edits: [] });

  while (heap.length > 0) {
    if (expansions >= expansionCap) return null;
    const c = pop();
    if (settled.has(settledKeyOf(c))) continue;
    settled.add(settledKeyOf(c));
    expansions++;

    if (c.i === n) {
      const output: TokenType[] = [];
      for (let k = c.outLen - 1; k >= 0; k--) {
        output.push(OUT_TYPES[Math.floor(c.outNum / 2 ** (4 * k)) % 16]);
      }
      if (parses(output)) return { cost: c.cost, edits: c.edits, output, expansions };
    }

    if (c.i < n) {
      const type = tokens[c.i].type;
      // Keep: always allowed here; grammaticality is checked at the end.
      const d = DELTA[type] ?? [0, 0, 0, 0];
      tryPush({
        i: c.i + 1,
        outNum: c.outNum * 16 + OUT_INDEX.get(type)!,
        outLen: c.outLen + 1,
        openP: c.openP + d[0],
        closeP: c.closeP + d[1],
        openB: c.openB + d[2],
        closeB: c.closeB + d[3],
        justDeleted: false,
        cost: c.cost,
        key: c.key,
        edits: c.edits,
      });
      if (c.cost + 1 <= costCap) {
        const edit: Edit = { kind: "delete", index: c.i };
        tryPush({
          i: c.i + 1,
          outNum: c.outNum,
          outLen: c.outLen,
          openP: c.openP,
          closeP: c.closeP,
          openB: c.openB,
          closeB: c.closeB,
          justDeleted: true,
          cost: c.cost + 1,
          key: c.key + encodeEdit(edit),
          edits: [...c.edits, edit],
        });
      }
    }
    if (!c.justDeleted && c.cost + 1 <= costCap) {
      for (const t of INSERTABLE) {
        const edit: Edit = { kind: "insert", gap: c.i, token: t };
        const d = DELTA[t] ?? [0, 0, 0, 0];
        tryPush({
          i: c.i,
          outNum: c.outNum * 16 + OUT_INDEX.get(t)!,
          outLen: c.outLen + 1,
          openP: c.openP + d[0],
          closeP: c.closeP + d[1],
          openB: c.openB + d[2],
          closeB: c.closeB + d[3],
          justDeleted: false,
          cost: c.cost + 1,
          key: c.key + encodeEdit(edit),
          edits: [...c.edits, edit],
        });
      }
    }
  }
  return null;
}
