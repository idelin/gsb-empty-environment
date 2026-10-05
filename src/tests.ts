import { Patch } from './model';
import { applyPatch } from './apply';
import { rebasePatch } from './rebase';

/** Targeted unit tests for the specified behaviours. */

let failures = 0;

function eq(actual: unknown, expected: unknown, name: string) {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a === e) {
    console.log(`ok   - ${name}`);
  } else {
    failures++;
    console.log(`FAIL - ${name}\n       expected: ${e}\n       actual:   ${a}`);
  }
}

function patch(id: string, ...edits: Array<[string, number, number, string]>): Patch {
  return {
    id,
    edits: edits.map(([eid, start, end, text]) => ({ id: eid, patchId: id, start, end, text })),
  };
}

// 1. Independent edits: the rebased edit shifts past the other patch.
{
  const A = patch('A', ['a1', 1, 3, 'X']); // "abcdef" -> "aXdef"
  const B = patch('B', ['b1', 4, 5, '']); // deletes 'e'
  const r = rebasePatch(B, A, 'left-priority', 6);
  eq(r.patch.edits.map((e) => [e.start, e.end, e.text]), [[3, 4, '']], 'independent edit shifts');
  eq(r.dropped, [], 'nothing dropped');
  eq(applyPatch(applyPatch('abcdef', A).text, r.patch).text, 'aXdf', 'rebased result text');
}

// 2. Same-point inserts are ordered by patch id, whichever applies first.
{
  const mk = (idA: string, idB: string): [Patch, Patch] => [
    patch(idA, ['a1', 1, 1, 'L']),
    patch(idB, ['b1', 1, 1, 'R']),
  ];
  for (const [idA, idB, want] of [
    ['a', 'b', 'aLRb'], // a < b -> L first
    ['z', 'y', 'aRLb'], // y < z -> R first
  ] as const) {
    const [A, B] = mk(idA, idB);
    const rBA = rebasePatch(B, A, 'left-priority', 2);
    const rAB = rebasePatch(A, B, 'left-priority', 2);
    const s1 = applyPatch(applyPatch('ab', A).text, rBA.patch).text;
    const s2 = applyPatch(applyPatch('ab', B).text, rAB.patch).text;
    eq([s1, s2], [want, want], `same-point inserts ordered by patch id (${idA} vs ${idB})`);
  }
}

// 3. Overlapping intervals, left-priority: left wins, edit reported, not lost.
{
  const A = patch('A', ['a1', 1, 4, 'X']);
  const B = patch('B', ['b1', 3, 5, 'Y']);
  const r = rebasePatch(B, A, 'left-priority', 6);
  eq(r.patch.edits, [], 'overlapping edit not preserved');
  eq(
    r.dropped.map((d) => [d.edit.id, d.reason, d.overlapsWith]),
    [['b1', 'left-priority-override', ['a1']]],
    'dropped edit reported with reason',
  );
  eq(applyPatch('abcdef', A).text, 'aXef', 'left patch applied');
}

// 4. Overlapping intervals, conflict policy: reported as conflict.
{
  const A = patch('A', ['a1', 1, 4, 'X']);
  const B = patch('B', ['b1', 3, 5, 'Y']);
  const r = rebasePatch(B, A, 'conflict', 6);
  eq(r.conflicts.length, 1, 'one conflict reported');
  eq(r.conflicts[0]!.reason, 'conflict', 'conflict reason');
  eq(r.conflicts[0]!.edit, B.edits[0]!, 'original edit preserved in report');
}

// 5. Insert strictly inside a replaced interval conflicts; at the boundary it does not.
{
  const A = patch('A', ['a1', 1, 4, 'X']);
  const inside = patch('B', ['b1', 2, 2, 'i']);
  const atStart = patch('B', ['b1', 1, 1, 'i']);
  const atEnd = patch('B', ['b1', 4, 4, 'i']);
  eq(rebasePatch(inside, A, 'conflict', 6).conflicts.length, 1, 'interior insert conflicts');
  eq(rebasePatch(atStart, A, 'left-priority', 6).dropped.length, 0, 'insert at start boundary survives');
  eq(rebasePatch(atEnd, A, 'left-priority', 6).dropped.length, 0, 'insert at end boundary survives');
  // boundary inserts land before/after the replacement text
  eq(applyPatch(applyPatch('abcdef', A).text, rebasePatch(atStart, A, 'left-priority', 6).patch).text, 'aiXef', 'insert before replacement');
  eq(applyPatch(applyPatch('abcdef', A).text, rebasePatch(atEnd, A, 'left-priority', 6).patch).text, 'aXief', 'insert after replacement');
}

// 6. Provenance (source mapping) of a simple apply.
{
  const P = patch('P', ['p1', 1, 1, 'XY']);
  const r = applyPatch('ab', P);
  eq(r.text, 'aXYb', 'apply text');
  eq(
    r.provenance,
    [
      { kind: 'base', index: 0 },
      { kind: 'edit', patchId: 'P', editId: 'p1', offset: 0 },
      { kind: 'edit', patchId: 'P', editId: 'p1', offset: 1 },
      { kind: 'base', index: 1 },
    ],
    'provenance tracks base chars and edit chars',
  );
}

// 7. No silent drops: every source edit is either in the output or reported.
{
  const A = patch('A', ['a1', 0, 2, 'X'], ['a2', 3, 3, 'i']);
  const B = patch('B', ['b1', 1, 1, 'j'], ['b2', 2, 3, 'Y'], ['b3', 4, 4, 'k']);
  const r = rebasePatch(B, A, 'left-priority', 5);
  eq(r.patch.edits.length + r.dropped.length, B.edits.length, 'all edits accounted for');
  eq(r.dropped.map((d) => d.edit.id), ['b1'], 'only the interior insert dropped');
}

// 8. Provenance is composed across a chain of applies.
{
  const A = patch('A', ['a1', 0, 0, 'X']);
  const B = patch('B', ['b1', 0, 0, 'Y']); // same point; A.id < B.id -> X then Y
  const rBA = rebasePatch(B, A, 'left-priority', 0);
  const chained = applyPatch(applyPatch('', A), rBA.patch);
  eq(chained.text, 'XY', 'chained apply text');
  eq(
    chained.provenance,
    [
      { kind: 'edit', patchId: 'A', editId: 'a1', offset: 0 },
      { kind: 'edit', patchId: 'B', editId: 'b1', offset: 0 },
    ],
    'provenance survives a chain of applies',
  );
}

if (failures > 0) {
  console.error(`\n${failures} test(s) failed`);
  process.exit(1);
}
console.log('\nAll targeted tests passed.');
