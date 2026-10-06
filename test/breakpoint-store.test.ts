import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BreakpointStore } from '../src/index.ts';

const V1 = [
  'function main() {',
  '  const a = 1;',
  '  const b = 2;',
  '  const result = compute(a, b);',
  '  console.log(result);',
  '  return result;',
  '}',
].join('\n');

const withLinesInsertedAbove = (src: string, count: number): string =>
  Array(count).fill('// filler').join('\n') + '\n' + src;

test('anchors are carried through an edit chain', () => {
  const store = new BreakpointStore(V1);
  const bp = store.addBreakpoint(4, 3);
  assert.equal(bp.status, 'bound');
  assert.equal(bp.line, 4);

  store.applyEdit(withLinesInsertedAbove(V1, 3));
  let now = store.getBreakpoint(bp.id);
  assert.equal(now?.status, 'bound');
  assert.equal(now?.line, 7);

  store.applyEdit(withLinesInsertedAbove(V1, 1));
  now = store.getBreakpoint(bp.id);
  assert.equal(now?.status, 'bound');
  assert.equal(now?.line, 5);
  assert.equal(store.getVersion(), 3);
});

test('deleting the statement unbinds the breakpoint instead of moving it to an unrelated line', () => {
  const store = new BreakpointStore(V1);
  const bp = store.addBreakpoint(4, 3);

  const deleted = [
    'function main() {',
    '  const a = 1;',
    '  const b = 2;',
    '  console.log(result);',
    '  return result;',
    '}',
  ].join('\n');
  store.applyEdit(deleted);

  const now = store.getBreakpoint(bp.id);
  assert.equal(now?.status, 'unbound');
  assert.equal(now?.statementId, null);
  // Keeps last known position; does not jump to `console.log` now on line 4.
  assert.equal(now?.line, 4);
  assert.notEqual(now?.statementKey, '');
});

test('moving a statement makes the breakpoint follow it (stable identity)', () => {
  const store = new BreakpointStore(V1);
  const bp = store.addBreakpoint(4, 3);
  const sid = bp.statementId;

  const moved = [
    'function main() {',
    '  const a = 1;',
    '  const b = 2;',
    '  console.log(result);',
    '  const result = compute(a, b);',
    '  return result;',
    '}',
  ].join('\n');
  store.applyEdit(moved);

  const now = store.getBreakpoint(bp.id);
  assert.equal(now?.status, 'bound');
  assert.equal(now?.line, 5);
  assert.equal(now?.statementId, sid);
});

test('copying a statement keeps the breakpoint on the original occurrence', () => {
  const store = new BreakpointStore(V1);
  const bp = store.addBreakpoint(4, 3);
  const sid = bp.statementId;

  const copied = [
    'function main() {',
    '  const a = 1;',
    '  const b = 2;',
    '  const result = compute(a, b);',
    '  const result = compute(a, b);',
    '  console.log(result);',
    '  return result;',
    '}',
  ].join('\n');
  store.applyEdit(copied);

  const now = store.getBreakpoint(bp.id);
  assert.equal(now?.status, 'bound');
  assert.equal(now?.line, 4);
  assert.equal(now?.statementId, sid);

  // The copy received a distinct identity.
  const sids = store.getStatements()
    .filter((s) => s.key === now?.statementKey)
    .map((s) => s.sid);
  assert.equal(sids.length, 2);
  assert.ok(sids.includes(sid as number));
});

test('delete-then-restore rebinds the breakpoint at the restored location', () => {
  const store = new BreakpointStore(V1);
  const bp = store.addBreakpoint(4, 3);

  const deleted = [
    'function main() {',
    '  const a = 1;',
    '  const b = 2;',
    '  console.log(result);',
    '  return result;',
    '}',
  ].join('\n');
  store.applyEdit(deleted);
  assert.equal(store.getBreakpoint(bp.id)?.status, 'unbound');

  // Restored at a different line (two extra lines inserted above).
  store.applyEdit(withLinesInsertedAbove(V1, 2));
  const now = store.getBreakpoint(bp.id);
  assert.equal(now?.status, 'bound');
  assert.equal(now?.line, 6);
});

test('deleting one of two identical copies does not rebind to the survivor', () => {
  const duplicated = [
    'foo();',
    'bar();',
    'foo();',
  ].join('\n');
  const store = new BreakpointStore(duplicated);
  // Bind to the second `foo();` (line 3).
  const bp = store.addBreakpoint(3, 1);
  assert.equal(bp.status, 'bound');
  assert.equal(bp.line, 3);

  // Delete the second copy; the first copy still exists with the same key.
  store.applyEdit(['foo();', 'bar();'].join('\n'));
  const now = store.getBreakpoint(bp.id);
  assert.equal(now?.status, 'unbound');
  assert.equal(now?.line, 3);
});

test('cross-version mapping exposes breakpoint state at each version', () => {
  const store = new BreakpointStore(V1);
  const bp = store.addBreakpoint(4, 3);

  store.applyEdit(withLinesInsertedAbove(V1, 2)); // v2: shifted to line 6
  const deleted = [
    'function main() {',
    '  const a = 1;',
    '  const b = 2;',
    '  console.log(result);',
    '  return result;',
    '}',
  ].join('\n');
  store.applyEdit(withLinesInsertedAbove(deleted, 2)); // v3: statement gone

  assert.equal(store.getVersion(), 3);

  const v1 = store.getBreakpointAtVersion(bp.id, 1);
  assert.equal(v1?.status, 'bound');
  assert.equal(v1?.line, 4);

  const v2 = store.getBreakpointAtVersion(bp.id, 2);
  assert.equal(v2?.status, 'bound');
  assert.equal(v2?.line, 6);

  const v3 = store.getBreakpointAtVersion(bp.id, 3);
  assert.equal(v3?.status, 'unbound');
  assert.equal(v3?.line, 6); // last known position, not remapped
});
