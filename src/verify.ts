import { Edit, Patch, Provenance } from './model';
import { applyPatch, validatePatch } from './apply';
import { rebasePatch } from './rebase';

/**
 * Exhaustive convergence check.
 *
 * For every short base string and every pair of patches (A, B) based on it,
 * rebasing must satisfy:
 *
 *   1. No edit is silently lost:
 *        rebase(X, Y).patch.edits.length + rebase(X, Y).dropped.length
 *          === X.edits.length
 *   2. The rebased patch is always well-formed against the intermediate text.
 *   3. If neither direction reports a dropped/conflicting edit, the two
 *      application orders converge, including the source mapping:
 *        apply(apply(base, A), rebase(B, A).patch)
 *          === apply(apply(base, B), rebase(A, B).patch)
 *      both as text and as provenance sequences.
 *   4. The 'conflict' policy flags exactly the same edits that
 *      'left-priority' drops, and produces the same surviving patch.
 */

const ALPHABET = ['a', 'b'];
// Override with VERIFY_MAX_LEN=N for a quicker/slower run.
const MAX_BASE_LEN = Number(process.env.VERIFY_MAX_LEN ?? 4);
const EDIT_TEXTS = ['', 'x', 'yz'];

function allStrings(alphabet: string[], maxLen: number): string[] {
  let level: string[] = [''];
  const out: string[] = [];
  for (let l = 0; l <= maxLen; l++) {
    out.push(...level);
    level = level.flatMap((s) => alphabet.map((c) => s + c));
  }
  return out;
}

interface EditSpec {
  start: number;
  end: number;
  text: string;
}

function editPool(n: number): EditSpec[] {
  const pool: EditSpec[] = [];
  for (let s = 0; s <= n; s++) {
    for (let e = s; e <= n; e++) {
      for (const text of EDIT_TEXTS) {
        if (s === e && text === '') continue; // no-op
        pool.push({ start: s, end: e, text });
      }
    }
  }
  return pool;
}

function compatible(u: EditSpec, v: EditSpec): boolean {
  return u.end <= v.start || v.end <= u.start;
}

/** All patches of size 0..2 over the pool (edits stored sorted). */
function allPatches(pool: EditSpec[]): EditSpec[][] {
  const patches: EditSpec[][] = [[]];
  for (const e of pool) patches.push([e]);
  for (let i = 0; i < pool.length; i++) {
    for (let j = i + 1; j < pool.length; j++) {
      const u = pool[i]!;
      const v = pool[j]!;
      if (!compatible(u, v)) continue;
      patches.push(u.start <= v.start ? [u, v] : [v, u]);
    }
  }
  return patches;
}

function buildPatch(id: string, specs: EditSpec[]): Patch {
  return {
    id,
    edits: specs.map((s, i) => ({
      id: `e${i}`,
      patchId: id,
      start: s.start,
      end: s.end,
      text: s.text,
    })),
  };
}

function provKey(p: Provenance): string {
  return p.kind === 'base' ? `b${p.index}` : `e${p.patchId}/${p.editId}/${p.offset}`;
}

function fail(msg: string, ctx: object): never {
  console.error('VERIFICATION FAILED:', msg);
  console.error(JSON.stringify(ctx, null, 2));
  process.exit(1);
}

let pairsChecked = 0;
let converged = 0;
let conflictedPairs = 0;
let editsPreserved = 0;
let editsDropped = 0;

function checkPair(base: string, specsA: EditSpec[], specsB: EditSpec[], idA: string, idB: string) {
  const A = buildPatch(idA, specsA);
  const B = buildPatch(idB, specsB);

  const rBA = rebasePatch(B, A, 'left-priority', base.length); // B after A
  const rAB = rebasePatch(A, B, 'left-priority', base.length); // A after B
  pairsChecked++;

  // (1) no edit is silently lost
  for (const [src, r] of [
    [B, rBA],
    [A, rAB],
  ] as const) {
    if (r.patch.edits.length + r.dropped.length !== src.edits.length) {
      fail('edit lost during rebase', { base, src, result: r });
    }
    if (r.conflicts.length !== 0) {
      fail('left-priority policy must not report conflicts', { base, result: r });
    }
  }

  // (2) rebased patches are well-formed against the intermediate texts
  const afterA = applyPatch(base, A);
  const afterB = applyPatch(base, B);
  try {
    validatePatch(rBA.patch, afterA.text.length);
    validatePatch(rAB.patch, afterB.text.length);
  } catch (err) {
    fail('rebased patch is malformed', { base, A, B, err: String(err) });
  }

  // (4) 'conflict' policy flags exactly the dropped edits, same survivors
  for (const [src, onto, r] of [
    [B, A, rBA],
    [A, B, rAB],
  ] as const) {
    const rc = rebasePatch(src, onto, 'conflict', base.length);
    if (JSON.stringify(rc.patch) !== JSON.stringify(r.patch)) {
      fail('conflict policy changed the surviving patch', { base, src, onto });
    }
    if (rc.conflicts.length !== r.dropped.length) {
      fail('conflict policy flagged a different edit set', { base, src, onto });
    }
    for (const d of rc.dropped) {
      if (d.reason !== 'conflict') {
        fail('conflict policy reported a wrong reason', { base, dropped: d });
      }
    }
  }

  editsPreserved += rBA.patch.edits.length + rAB.patch.edits.length;
  editsDropped += rBA.dropped.length + rAB.dropped.length;

  // (3) convergence when conflict-free, text AND provenance
  if (rBA.dropped.length === 0 && rAB.dropped.length === 0) {
    // Thread the AppliedText through so provenance refers to the original
    // base text and edits across both applies.
    const s1 = applyPatch(afterA, rBA.patch);
    const s2 = applyPatch(afterB, rAB.patch);
    if (s1.text !== s2.text) {
      fail('orders diverge on text', { base, A, B, s1: s1.text, s2: s2.text });
    }
    const p1 = s1.provenance.map(provKey).join(',');
    const p2 = s2.provenance.map(provKey).join(',');
    if (p1 !== p2) {
      fail('orders diverge on provenance', { base, A, B, p1, p2 });
    }
    converged++;
  } else {
    conflictedPairs++;
  }
}

const t0 = Date.now();
for (const base of allStrings(ALPHABET, MAX_BASE_LEN)) {
  const patches = allPatches(editPool(base.length));
  for (let i = 0; i < patches.length; i++) {
    for (let j = i; j < patches.length; j++) {
      // Exercise both patch-id orderings: same-point inserts are ordered by
      // patch id, so convergence must hold whichever id is smaller.
      checkPair(base, patches[i]!, patches[j]!, 'a', 'b');
      checkPair(base, patches[i]!, patches[j]!, 'b', 'a');
    }
  }
}

console.log('Exhaustive verification passed.');
console.log(`  base strings      : ${allStrings(ALPHABET, MAX_BASE_LEN).length} (alphabet ${ALPHABET.join('')}, len <= ${MAX_BASE_LEN})`);
console.log(`  patch pairs       : ${pairsChecked} (both id orderings)`);
console.log(`  conflict-free     : ${converged} (text + provenance converged)`);
console.log(`  with conflicts    : ${conflictedPairs} (dropped edits reported)`);
console.log(`  edits preserved   : ${editsPreserved}`);
console.log(`  edits dropped     : ${editsDropped} (all reported, none silent)`);
console.log(`  elapsed           : ${Date.now() - t0} ms`);
