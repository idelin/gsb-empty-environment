import assert from 'node:assert/strict';
import { BreakpointStore, LineIndex } from '../src/index.js';

const SRC = 'const a = 1;\nconst b = 2;\nconst c = 3;\n';

const lineStart = (src, line) => new LineIndex(src).positionToOffset(line, 1);
const deleteLine = (src, line) => ({
  start: lineStart(src, line),
  end: line === src.split('\n').length ? src.length : lineStart(src, line + 1),
  text: '',
});
const onlyBp = (store) => {
  const bps = store.getBreakpoints();
  assert.equal(bps.length, 1);
  return bps[0];
};

test('edit chain: inserting lines above shifts a bound breakpoint', () => {
  const store = new BreakpointStore(SRC);
  const bp = store.setBreakpoint(2, 3);
  assert.equal(bp.status, 'bound');
  assert.equal(bp.line, 2);

  store.applyEdit({ start: 0, end: 0, text: '// one\n// two\n' });
  const after = onlyBp(store);
  assert.equal(after.status, 'bound');
  assert.equal(after.line, 4);
  assert.equal(after.statementId, bp.statementId);
});

test('edit chain: editing inside the statement keeps identity', () => {
  const store = new BreakpointStore(SRC);
  const bp = store.setBreakpoint(2, 3);
  const at = store.getSource().indexOf('b = 2');
  store.applyEdit({ start: at, end: at + 5, text: 'beta = 20' });
  const after = onlyBp(store);
  assert.equal(after.status, 'bound');
  assert.equal(after.statementId, bp.statementId);
  assert.equal(after.line, 2);
  assert.ok(store.getSource().includes('const beta = 20;'));
});

test('delete: breakpoint becomes unbound instead of sliding to an unrelated line', () => {
  const store = new BreakpointStore(SRC);
  store.setBreakpoint(2, 3);
  store.applyEdit(deleteLine(store.getSource(), 2));
  const after = onlyBp(store);
  assert.equal(after.status, 'unbound');
  assert.equal(after.line, null);
  assert.equal(after.column, null);
  // Last known position is retained for display only.
  assert.deepEqual({ line: after.lastKnown.line, column: after.lastKnown.column }, { line: 2, column: 3 });

  // Further edits must not resurrect it onto some other statement.
  store.applyEdit({ start: 0, end: 0, text: '// more\n'.repeat(3) });
  const later = onlyBp(store);
  assert.equal(later.status, 'unbound');
  assert.equal(later.line, null);
});

test('delete: identical sibling statements do not steal the breakpoint', () => {
  const store = new BreakpointStore('foo();\nfoo();\n');
  store.setBreakpoint(1, 2);
  store.applyEdit(deleteLine(store.getSource(), 1));
  const after = onlyBp(store);
  assert.equal(after.status, 'unbound');
  assert.equal(after.line, null);
});

test('delete + restore (undo): breakpoint rebinds to the revived statement id', () => {
  const store = new BreakpointStore(SRC);
  const bp = store.setBreakpoint(2, 3);
  const cut = deleteLine(store.getSource(), 2);
  const removed = store.getSource().slice(cut.start, cut.end);
  store.applyEdit(cut);
  assert.equal(onlyBp(store).status, 'unbound');

  store.applyEdit({ start: cut.start, end: cut.start, text: removed });
  const after = onlyBp(store);
  assert.equal(after.status, 'bound');
  assert.equal(after.line, 2);
  assert.equal(after.statementId, bp.statementId);
});

test('move: cut and paste across two edits follows the statement', () => {
  const store = new BreakpointStore(SRC);
  const bp = store.setBreakpoint(2, 3);
  const cut = deleteLine(store.getSource(), 2);
  const moved = store.getSource().slice(cut.start, cut.end);
  store.applyEdit(cut);
  assert.equal(onlyBp(store).status, 'unbound');

  store.applyEdit({ start: store.getSource().length, end: store.getSource().length, text: moved });
  const after = onlyBp(store);
  assert.equal(after.status, 'bound');
  assert.equal(after.line, 3);
  assert.equal(after.statementId, bp.statementId);
});

test('move: a single batch edit keeps identity in one version step', () => {
  const store = new BreakpointStore(SRC);
  const bp = store.setBreakpoint(2, 3);
  const cut = deleteLine(SRC, 2);
  const moved = SRC.slice(cut.start, cut.end);
  const v0 = store.getVersion();
  store.applyEdits([
    cut,
    { start: SRC.length, end: SRC.length, text: moved },
  ]);
  assert.equal(store.getVersion(), v0 + 1);
  const after = onlyBp(store);
  assert.equal(after.status, 'bound');
  assert.equal(after.line, 3);
  assert.equal(after.statementId, bp.statementId);
});

test('copy: duplicating a statement leaves the breakpoint on the original', () => {
  const store = new BreakpointStore(SRC);
  const bp = store.setBreakpoint(2, 3);
  // Paste a copy of line 2 right below it.
  store.applyEdit({ start: lineStart(SRC, 3), end: lineStart(SRC, 3), text: 'const b = 2;\n' });
  const after = onlyBp(store);
  assert.equal(after.status, 'bound');
  assert.equal(after.line, 2);
  assert.equal(after.statementId, bp.statementId);
  // The copy received a distinct statement id.
  const ids = store.getStatements().map((s) => s.id);
  assert.equal(new Set(ids).size, ids.length);
  assert.equal(store.getStatements().length, 4);
});

test('copy: breakpoint on the original survives deletion of the copy', () => {
  const store = new BreakpointStore(SRC);
  store.setBreakpoint(2, 3);
  store.applyEdit({ start: lineStart(SRC, 3), end: lineStart(SRC, 3), text: 'const b = 2;\n' });
  store.applyEdit(deleteLine(store.getSource(), 3));
  const after = onlyBp(store);
  assert.equal(after.status, 'bound');
  assert.equal(after.line, 2);
});

test('cross-version: breakpoints map to retained older and newer versions', () => {
  const store = new BreakpointStore(SRC);
  const bp = store.setBreakpoint(2, 3);
  store.applyEdit({ start: 0, end: 0, text: '// x\n// y\n' }); // v1: b at line 4
  store.applyEdit(deleteLine(store.getSource(), 4)); // v2: b deleted

  assert.deepEqual(
    pick(store.mapBreakpointToVersion(bp.id, 0)),
    { status: 'bound', line: 2, column: 3 },
  );
  assert.deepEqual(
    pick(store.mapBreakpointToVersion(bp.id, 1)),
    { status: 'bound', line: 4, column: 3 },
  );
  assert.equal(store.mapBreakpointToVersion(bp.id, 2).status, 'unbound');
  assert.equal(store.mapBreakpointToVersion(bp.id, 999), null);
});

test('cross-version: setSource maps breakpoints onto a rearranged revision', () => {
  const store = new BreakpointStore(SRC);
  const bp = store.setBreakpoint(2, 3);
  store.setSource('const c = 3;\nconst a = 1;\nconst b = 2;\n');
  const after = onlyBp(store);
  assert.equal(after.status, 'bound');
  assert.equal(after.line, 3);
  assert.equal(after.statementId, bp.statementId);
});

test('nested: breakpoints bind to the innermost statement and follow it', () => {
  const src = 'function f() {\n  const inner = 1;\n  return inner;\n}\nconst after = 2;\n';
  const store = new BreakpointStore(src);
  const bp = store.setBreakpoint(3, 3);
  assert.equal(bp.status, 'bound');
  assert.equal(store.getStatementAt(3, 3).kind, 'return');

  // Insert a line inside the function, above the return.
  const at = lineStart(src, 3);
  store.applyEdit({ start: at, end: at, text: '  log(inner);\n' });
  const afterBp = onlyBp(store);
  assert.equal(afterBp.status, 'bound');
  assert.equal(afterBp.line, 4);
  assert.equal(afterBp.statementId, bp.statementId);
});

test('events: version events report rebound and unbound breakpoints', () => {
  const store = new BreakpointStore(SRC);
  store.setBreakpoint(2, 3);
  const events = [];
  store.subscribe((e) => events.push(e));
  store.applyEdit(deleteLine(store.getSource(), 2));
  const versionEvents = events.filter((e) => e.type === 'version');
  assert.equal(versionEvents.length, 1);
  assert.equal(versionEvents[0].unboundBreakpointIds.length, 1);
  assert.equal(versionEvents[0].reboundBreakpointIds.length, 0);
});

function pick(v) {
  return { status: v.status, line: v.line, column: v.column };
}

test('typescript: return-type annotated methods expose inner statements', () => {
  const src = 'class Repo {\n  add(item: number): void {\n    this.items.push(item);\n  }\n}\n';
  const store = new BreakpointStore(src);
  const bp = store.setBreakpoint(3, 20);
  assert.equal(bp.status, 'bound');
  assert.equal(store.getStatementAt(3, 20).key, 'expression:this . items . push ( item ) ;');

  // Deleting the inner statement unbinds; it must not re-anchor to the method.
  const li = new LineIndex(store.getSource());
  store.applyEdit({ start: li.positionToOffset(3, 1), end: li.positionToOffset(4, 1), text: '' });
  assert.equal(onlyBp(store).status, 'unbound');

  // Pasting the statement back elsewhere rebinds it (tombstone revival).
  store.applyEdit({ start: store.getSource().length, end: store.getSource().length, text: '    this.items.push(item);\n' });
  const after = onlyBp(store);
  assert.equal(after.status, 'bound');
  assert.equal(after.statementId, bp.statementId);
});
