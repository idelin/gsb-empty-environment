import type { DecisionTable, Scalar } from '../src/model.ts';
import { fieldDomain, ruleBox } from '../src/boxes.ts';

export interface BruteForceResult {
  // ruleId -> true if some input makes it the unique top-priority match
  winnable: Set<string>;
  // "idA|idB" (sorted) for equal-priority pairs that tie at the top on some input
  effectiveConflicts: Set<string>;
  // the unique winner for each enumerated input (null on tie / no match)
  winnerOf(input: Record<string, Scalar>): string | null;
}

export function bruteForce(table: DecisionTable): BruteForceResult {
  const fields = table.fields;
  const domains = fields.map((f) => fieldDomain(f));
  const boxes = table.rules.map((r) => ruleBox(table, r));
  const priorities = table.rules.map((r) => r.priority ?? 0);

  const winnable = new Set<string>();
  const effectiveConflicts = new Set<string>();

  const matches = (input: Record<string, Scalar>): number[] => {
    const out: number[] = [];
    for (let i = 0; i < table.rules.length; i++) {
      let ok = true;
      for (const f of fields) {
        if (!boxes[i].get(f.name)!.has(input[f.name])) { ok = false; break; }
      }
      if (ok) out.push(i);
    }
    return out;
  };

  const winnerOf = (input: Record<string, Scalar>): string | null => {
    const hits = matches(input);
    if (hits.length === 0) return null;
    const top = Math.max(...hits.map((i) => priorities[i]));
    const tops = hits.filter((i) => priorities[i] === top);
    return tops.length === 1 ? table.rules[tops[0]].id : null;
  };

  const input: Record<string, Scalar> = {};
  const walk = (fi: number) => {
    if (fi === fields.length) {
      const hits = matches(input);
      if (hits.length === 0) return;
      const top = Math.max(...hits.map((i) => priorities[i]));
      const tops = hits.filter((i) => priorities[i] === top);
      if (tops.length === 1) {
        winnable.add(table.rules[tops[0]].id);
      } else {
        for (let a = 0; a < tops.length; a++) {
          for (let b = a + 1; b < tops.length; b++) {
            const pair = [table.rules[tops[a]].id, table.rules[tops[b]].id].sort();
            effectiveConflicts.add(pair.join('|'));
          }
        }
      }
      return;
    }
    for (const v of domains[fi]) {
      input[fields[fi].name] = v;
      walk(fi + 1);
    }
  };
  walk(0);

  return { winnable, effectiveConflicts, winnerOf };
}

// Deterministic PRNG (mulberry32) for reproducible random tables.
export function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
