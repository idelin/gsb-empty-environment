import type { Edit, Patch } from './model.ts';
import { applyPatch } from './model.ts';
import type { RebaseResult, Strategy } from './rebase.ts';
import { rebase } from './rebase.ts';

export interface VerifyOptions {
  /** Maximum base-string length (inclusive). Default 3. */
  maxLength?: number;
  /** Alphabet for base strings. Default ['a', 'b']. */
  alphabet?: string[];
  /** Replacement texts used by generated edits. Default ['', 'x']. */
  replacements?: string[];
  /** Maximum edits per generated patch. Default 2. */
  maxEditsPerPatch?: number;
}

export interface VerifyStats {
  strings: number;
  patches: number;
  pairs: number;
  conflictFreePairs: number;
  conflictedPairs: number;
  editsRebased: number;
  editsUnpreserved: number;
}

function allStrings(alphabet: string[], maxLength: number): string[] {
  const out: string[] = [''];
  let level: string[] = [''];
  for (let len = 1; len <= maxLength; len++) {
    const next: string[] = [];
    for (const s of level) for (const ch of alphabet) next.push(s + ch);
    out.push(...next);
    level = next;
  }
  return out;
}

/** All non-trivial single edits over `base` using the given replacements. */
function candidateEdits(base: string, replacements: string[]): Edit[] {
  const edits: Edit[] = [];
  let n = 0;
  for (let start = 0; start <= base.length; start++) {
    for (let end = start; end <= base.length; end++) {
      for (const text of replacements) {
        if (text === base.slice(start, end)) continue; // no-op edit
        edits.push({ id: `e${n++}`, start, end, text });
      }
    }
  }
  return edits;
}

function compatible(first: Edit, second: Edit): boolean {
  // Sorted and non-overlapping; same-offset insertions apply in array order.
  return second.start >= first.end;
}

/** All patches over `base` with 0..maxEdits edits (edits shared, not copied). */
function allPatches(base: string, replacements: string[], maxEdits: number): Edit[][] {
  const edits = candidateEdits(base, replacements);
  const patches: Edit[][] = [[]];
  if (maxEdits >= 1) {
    for (const e of edits) patches.push([e]);
  }
  if (maxEdits >= 2) {
    for (let i = 0; i < edits.length; i++) {
      for (let j = i + 1; j < edits.length; j++) {
        if (compatible(edits[i], edits[j])) patches.push([edits[i], edits[j]]);
      }
    }
  }
  return patches;
}

function assertConservation(right: Patch, result: RebaseResult, context: string): void {
  const kept = result.patch.edits.length;
  const dropped = result.unpreserved.length;
  if (kept + dropped !== right.edits.length) {
    throw new Error(
      `${context}: conservation violated: ${kept} kept + ${dropped} unpreserved != ${right.edits.length} original`,
    );
  }
  const byId = new Map(right.edits.map((e) => [e.id, e]));
  const seen = new Set<string>();
  for (const rebased of result.patch.edits) {
    if (rebased.origin.patchId !== right.id) {
      throw new Error(`${context}: wrong origin patch id ${rebased.origin.patchId}`);
    }
    const original = byId.get(rebased.origin.editId);
    if (original === undefined) {
      throw new Error(`${context}: origin ${rebased.origin.editId} not in source patch`);
    }
    if (seen.has(rebased.origin.editId)) {
      throw new Error(`${context}: edit ${rebased.origin.editId} duplicated by rebase`);
    }
    seen.add(rebased.origin.editId);
    if (rebased.text !== original.text) {
      throw new Error(`${context}: edit ${original.id} text changed during rebase`);
    }
    if (original.start === original.end && rebased.start !== rebased.end) {
      throw new Error(`${context}: insertion ${original.id} became a range edit`);
    }
  }
  for (const un of result.unpreserved) {
    if (!byId.has(un.edit.id)) {
      throw new Error(`${context}: unpreserved edit ${un.edit.id} not in source patch`);
    }
    if (seen.has(un.edit.id)) {
      throw new Error(`${context}: edit ${un.edit.id} both kept and unpreserved`);
    }
    seen.add(un.edit.id);
  }
}

function describe(patch: Patch): string {
  const parts = patch.edits.map((e) => `${e.id}:[${e.start},${e.end})->${JSON.stringify(e.text)}`);
  return `${patch.id}{${parts.join(', ')}}`;
}

/**
 * Exhaustively verifies, over all short base strings and all generated patch
 * pairs, that:
 *  1. conservation: no edit is ever silently dropped (kept + unpreserved =
 *     original, origins form a bijection onto the preserved edits);
 *  2. convergence: when both rebase directions are conflict-free,
 *     apply(apply(S, A), rebase(A, B)) === apply(apply(S, B), rebase(B, A));
 *  3. 'left-priority' and 'conflict' agree whenever there is no overlap.
 *
 * Every pair is checked under both patch-id orderings (left id smaller and
 * larger than right id) to exercise same-point insertion ordering.
 * Throws on the first counterexample; otherwise returns statistics.
 */
export function runExhaustive(options: VerifyOptions = {}): VerifyStats {
  const maxLength = options.maxLength ?? 3;
  const alphabet = options.alphabet ?? ['a', 'b'];
  const replacements = options.replacements ?? ['', 'x'];
  const maxEditsPerPatch = options.maxEditsPerPatch ?? 2;

  const stats: VerifyStats = {
    strings: 0,
    patches: 0,
    pairs: 0,
    conflictFreePairs: 0,
    conflictedPairs: 0,
    editsRebased: 0,
    editsUnpreserved: 0,
  };

  const strategies: Strategy[] = ['conflict', 'left-priority'];

  for (const base of allStrings(alphabet, maxLength)) {
    stats.strings++;
    const editLists = allPatches(base, replacements, maxEditsPerPatch);
    stats.patches += editLists.length;
    for (const editsA of editLists) {
      for (const editsB of editLists) {
        for (const leftId of ['pa', 'pz'] as const) {
          // leftId 'pa' < rightId 'pb'; leftId 'pz' > rightId 'pb'
          const rightId = 'pb';
          const a: Patch = { id: leftId, edits: editsA };
          const b: Patch = { id: rightId, edits: editsB };
          stats.pairs++;

          const forward = rebase(a, b, 'conflict');
          const backward = rebase(b, a, 'conflict');
          const ctx = `base=${JSON.stringify(base)} A=${describe(a)} B=${describe(b)}`;
          assertConservation(b, forward, `${ctx} (forward)`);
          assertConservation(a, backward, `${ctx} (backward)`);
          stats.editsRebased += forward.patch.edits.length + backward.patch.edits.length;
          stats.editsUnpreserved += forward.unpreserved.length + backward.unpreserved.length;

          if (forward.conflicts.length === 0 && backward.conflicts.length === 0) {
            stats.conflictFreePairs++;
            const viaA = applyPatch(applyPatch(base, a), forward.patch);
            const viaB = applyPatch(applyPatch(base, b), backward.patch);
            if (viaA !== viaB) {
              throw new Error(
                `convergence failure: ${ctx}\n  A-then-B: ${JSON.stringify(viaA)}\n  B-then-A: ${JSON.stringify(viaB)}`,
              );
            }
            // With no overlap, both strategies must produce identical output.
            for (const strategy of strategies) {
              const f = rebase(a, b, strategy);
              const g = rebase(b, a, strategy);
              if (JSON.stringify(f.patch) !== JSON.stringify(forward.patch)) {
                throw new Error(`strategy mismatch (forward, ${strategy}): ${ctx}`);
              }
              if (JSON.stringify(g.patch) !== JSON.stringify(backward.patch)) {
                throw new Error(`strategy mismatch (backward, ${strategy}): ${ctx}`);
              }
            }
          } else {
            stats.conflictedPairs++;
            // left-priority must report exactly the conflicting edits as
            // unpreserved and still conserve everything.
            const fLeft = rebase(a, b, 'left-priority');
            const bLeft = rebase(b, a, 'left-priority');
            assertConservation(b, fLeft, `${ctx} (forward, left-priority)`);
            assertConservation(a, bLeft, `${ctx} (backward, left-priority)`);
            if (fLeft.unpreserved.length !== forward.unpreserved.length) {
              throw new Error(`left-priority dropped a different set of edits: ${ctx}`);
            }
          }
        }
      }
    }
  }
  return stats;
}
