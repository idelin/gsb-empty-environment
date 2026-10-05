import { applyPatch } from './apply';
import { rebasePatch } from './rebase';

/**
 * Small end-to-end demo: two patches based on the same text, the second one
 * rebased past the first.
 */

const base = 'the quick brown fox';

// Patch A (ids sort before B's): two edits against `base`.
const A = {
  id: 'alpha',
  edits: [
    { id: 'a1', patchId: 'alpha', start: 4, end: 9, text: 'swift' }, // quick -> swift
    { id: 'a2', patchId: 'alpha', start: 19, end: 19, text: '!' }, // append '!'
  ],
};

// Patch B, against the same base.
const B = {
  id: 'beta',
  edits: [
    { id: 'b1', patchId: 'beta', start: 10, end: 15, text: 'red' }, // brown -> red
    { id: 'b2', patchId: 'beta', start: 19, end: 19, text: '?' }, // insert at same point as a2
    { id: 'b3', patchId: 'beta', start: 4, end: 8, text: 'QUICK' }, // overlaps a1
  ],
};

for (const policy of ['left-priority', 'conflict'] as const) {
  const rebased = rebasePatch(B, A, policy, base.length);
  const step1 = applyPatch(base, A);
  const step2 = applyPatch(step1, rebased.patch); // pass AppliedText to keep the source mapping
  console.log(`policy: ${policy}`);
  console.log(`  base            : ${base}`);
  console.log(`  after A         : ${step1.text}`);
  console.log(`  rebased B edits : ${JSON.stringify(rebased.patch.edits)}`);
  console.log(`  final           : ${step2.text}`);
  console.log(`  not preserved   : ${JSON.stringify(rebased.dropped)}`);
  console.log(
    `  provenance      : ${step2.provenance
      .map((p) => (p.kind === 'base' ? `b${p.index}` : `${p.patchId}/${p.editId}`))
      .join(' ')}`,
  );
  console.log();
}
