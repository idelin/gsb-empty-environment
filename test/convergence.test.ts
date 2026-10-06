/**
 * Convergence harness: for each scenario, apply the message set in EVERY
 * permutation order to a fresh replica and assert that
 *   1. every replica converges to the exact same snapshot + diagnostics,
 *   2. that state equals the order-independent reference computation,
 *   3. explicit expectations (final order, tombstones, missing-dependency
 *      diagnostics) hold independently of the reference implementation.
 */

import { ListModel, ROOT_ID } from '../src/list-model.js';
import type { Message } from '../src/list-model.js';
import { referenceDiagnostics, referenceSnapshot } from './reference.js';
import type { RefDiagnostics } from './reference.js';

let failures = 0;

function check(cond: boolean, label: string): void {
  if (!cond) {
    failures += 1;
    console.error('FAIL: ' + label);
  }
}

function checkEq(actual: unknown, expected: unknown, label: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) {
    failures += 1;
    console.error('FAIL: ' + label + '\n  expected: ' + e + '\n  actual:   ' + a);
  }
}

function permutations<T>(items: T[]): T[][] {
  if (items.length <= 1) return [items.slice()];
  const out: T[][] = [];
  for (let idx = 0; idx < items.length; idx += 1) {
    const rest = items.slice(0, idx).concat(items.slice(idx + 1));
    for (const tail of permutations(rest)) {
      out.push([items[idx]].concat(tail));
    }
  }
  return out;
}

function diagOf(model: ListModel<string>): RefDiagnostics {
  const d = model.diagnostics();
  const pending: string[] = [];
  for (const p of d.pending) pending.push(p.id);
  return {
    pending: pending,
    missingPredecessors: d.missingPredecessors,
    deletesAwaitingInsert: d.deletesAwaitingInsert,
  };
}

function runConvergence(name: string, msgs: Array<Message<string>>): void {
  const expectedSnap = referenceSnapshot(msgs);
  const expectedDiag = referenceDiagnostics(msgs);
  const expectedValues: string[] = [];
  for (const e of expectedSnap) {
    if (!e.deleted) expectedValues.push(e.value);
  }
  const perms = permutations(msgs);
  const seen = new Set<string>();
  for (const perm of perms) {
    const model = new ListModel<string>();
    model.applyAll(perm);
    const snap = model.snapshot();
    seen.add(JSON.stringify(snap));
    checkEq(snap, expectedSnap, name + ': snapshot matches reference');
    checkEq(diagOf(model), expectedDiag, name + ': diagnostics match reference');
    checkEq(model.values(), expectedValues, name + ': visible values');
  }
  check(seen.size === 1, name + ': all ' + perms.length + ' permutations converge');
  console.log('ok - ' + name + ' (' + perms.length + ' permutations, 1 converged state)');
}

function runHealing(name: string, msgs: Array<Message<string>>): void {
  const perms = permutations(msgs);
  const fullSnap = referenceSnapshot(msgs);
  const fullDiag = referenceDiagnostics(msgs);
  for (const perm of perms) {
    const last = perm[perm.length - 1];
    const prefix = perm.slice(0, perm.length - 1);
    const model = new ListModel<string>();
    model.applyAll(prefix);
    checkEq(diagOf(model), referenceDiagnostics(prefix), name + ': prefix diagnostics');
    model.apply(last);
    checkEq(model.snapshot(), fullSnap, name + ': healed snapshot');
    checkEq(diagOf(model), fullDiag, name + ': healed diagnostics');
  }
  console.log('ok - ' + name + ' (' + perms.length + ' permutations healed)');
}

const scenarioA: Array<Message<string>> = [
  { kind: 'insert', id: 'a', pred: ROOT_ID, value: 'A' },
  { kind: 'insert', id: 'b', pred: 'a', value: 'B' },
  { kind: 'insert', id: 'c', pred: 'a', value: 'C' },
  { kind: 'insert', id: 'd', pred: 'c', value: 'D' },
  { kind: 'delete', id: 'b' },
  { kind: 'insert', id: 'e', pred: 'b', value: 'E' },
];

function explicitA(): void {
  const model = new ListModel<string>();
  model.applyAll(scenarioA);
  checkEq(model.snapshot().map((e) => e.id), ['a', 'b', 'e', 'c', 'd'], 'A: explicit id order');
  checkEq(
    model.snapshot().map((e) => e.deleted),
    [false, true, false, false, false],
    'A: tombstone flags',
  );
  checkEq(model.values(), ['A', 'E', 'C', 'D'], 'A: visible values skip tombstone');
  checkEq(diagOf(model), { pending: [], missingPredecessors: [], deletesAwaitingInsert: [] }, 'A: no missing deps');
}

const scenarioB: Array<Message<string>> = [
  { kind: 'insert', id: 'a', pred: ROOT_ID, value: 'A' },
  { kind: 'insert', id: 'x', pred: 'ghost', value: 'X' },
  { kind: 'insert', id: 'y', pred: 'x', value: 'Y' },
  { kind: 'delete', id: 'z' },
];

function explicitB(): void {
  const model = new ListModel<string>();
  model.applyAll(scenarioB);
  checkEq(model.values(), ['A'], 'B: only resolvable inserts are visible');
  checkEq(
    diagOf(model),
    { pending: ['x', 'y'], missingPredecessors: ['ghost', 'x'], deletesAwaitingInsert: ['z'] },
    'B: missing-dependency diagnostics',
  );
}

const scenarioC: Array<Message<string>> = [
  { kind: 'insert', id: 'a', pred: ROOT_ID, value: 'A' },
  { kind: 'insert', id: 'a', pred: ROOT_ID, value: 'A' },
  { kind: 'insert', id: 'b', pred: 'a', value: 'B' },
  { kind: 'delete', id: 'b' },
  { kind: 'delete', id: 'b' },
  { kind: 'insert', id: 'c', pred: 'b', value: 'C' },
];

function explicitC(): void {
  const model = new ListModel<string>();
  model.applyAll(scenarioC);
  checkEq(model.snapshot().map((e) => e.id), ['a', 'b', 'c'], 'C: explicit id order');
  checkEq(model.snapshot().map((e) => e.deleted), [false, true, false], 'C: tombstone flags');
  checkEq(model.values(), ['A', 'C'], 'C: child of tombstone stays visible');
  checkEq(diagOf(model), { pending: [], missingPredecessors: [], deletesAwaitingInsert: [] }, 'C: no missing deps');
}

const scenarioF: Array<Message<string>> = [
  { kind: 'insert', id: 'm', pred: ROOT_ID, value: 'M' },
  { kind: 'insert', id: 'b9', pred: 'm', value: 'v-b9' },
  { kind: 'insert', id: 'b10', pred: 'm', value: 'v-b10' },
  { kind: 'insert', id: 'ba', pred: 'm', value: 'v-ba' },
];

function explicitF(): void {
  const model = new ListModel<string>();
  model.applyAll(scenarioF);
  checkEq(model.snapshot().map((e) => e.id), ['m', 'b10', 'b9', 'ba'], 'F: pure lexicographic sibling order');
}

runConvergence('A chain+branch+tombstone-anchor', scenarioA);
explicitA();
runConvergence('B missing-predecessor diagnostics', scenarioB);
explicitB();
runConvergence('C duplicates+delete-before-insert', scenarioC);
explicitC();
runConvergence('F lexicographic sibling order', scenarioF);
explicitF();
runHealing('D late dependency heals', scenarioA);

if (failures > 0) {
  throw new Error(failures + ' check(s) failed');
}
console.log('all convergence checks passed');
