import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  analyzeProject,
  applyRenamePatch,
  preflightRename,
  type Project,
} from '../src/index.ts';

function symbolsNamed(project: Project, name: string, file?: string) {
  return [...project.symbols.values()].filter(
    (s) => s.name === name && (file === undefined || s.file === file),
  );
}

test('closure with same-name variable: rename touches only the target symbol', () => {
  const src = [
    'let x = 1;',
    'fn f() {',
    '  let x = 2;',
    '  return x;',
    '}',
    'let y = x;',
    '',
  ].join('\n');
  const project = analyzeProject({ 'a.ml': src });
  const [outerX, innerX] = symbolsNamed(project, 'x');

  const renameOuter = preflightRename(project, outerX.id, 'outer');
  assert.ok(renameOuter.ok, JSON.stringify(renameOuter.conflicts));
  assert.equal(renameOuter.edits.length, 2);
  for (const edit of renameOuter.edits) {
    assert.equal(src.slice(edit.start, edit.end), 'x');
  }
  const appliedOuter = applyRenamePatch(project, renameOuter.patch!);
  assert.ok(appliedOuter.ok);
  const newSrc = appliedOuter.sources.get('a.ml')!;
  assert.match(newSrc, /let outer = 1;/);
  assert.match(newSrc, /let y = outer;/);
  assert.match(newSrc, /let x = 2;/);
  assert.match(newSrc, /return x;/);

  const renameInner = preflightRename(project, innerX.id, 'inner');
  assert.ok(renameInner.ok, JSON.stringify(renameInner.conflicts));
  assert.equal(renameInner.edits.length, 2);
  const appliedInner = applyRenamePatch(project, renameInner.patch!);
  assert.ok(appliedInner.ok);
  const innerSrc = appliedInner.sources.get('a.ml')!;
  assert.match(innerSrc, /let inner = 2;/);
  assert.match(innerSrc, /return inner;/);
  assert.match(innerSrc, /let x = 1;/);
});

test('capture conflict: renamed reference would be captured by an inner declaration', () => {
  const src = ['let y = 1;', 'fn f() {', '  let x = 2;', '  return y;', '}', ''].join('\n');
  const project = analyzeProject({ 'a.ml': src });
  const y = symbolsNamed(project, 'y')[0];
  const result = preflightRename(project, y.id, 'x');
  assert.equal(result.ok, false);
  assert.equal(result.conflicts.length, 1);
  const conflict = result.conflicts[0];
  assert.equal(conflict.reason, 'capture');
  assert.equal(src.slice(conflict.start, conflict.end), 'y');
});

test('shadow conflict: renamed declaration would steal references of an outer symbol', () => {
  const src = [
    'let y = 1;',
    'fn f() {',
    '  let x = 2;',
    '  return y;',
    '}',
    'let z = y;',
    '',
  ].join('\n');
  const project = analyzeProject({ 'a.ml': src });
  const innerX = symbolsNamed(project, 'x')[0];
  const result = preflightRename(project, innerX.id, 'y');
  assert.equal(result.ok, false);
  assert.equal(result.conflicts.length, 1);
  const conflict = result.conflicts[0];
  assert.equal(conflict.reason, 'shadow');
  assert.equal(src.slice(conflict.start, conflict.end), 'y');
  // The conflict is the `return y;` inside f, not the top-level `let z = y;`.
  assert.ok(conflict.start > src.indexOf('fn f'));
  assert.ok(conflict.end < src.indexOf('let z'));
});

test('reference before declaration: rename leaves the unbound reference untouched', () => {
  const src = ['fn g() { return x; }', 'let x = 1;', ''].join('\n');
  const project = analyzeProject({ 'a.ml': src });
  assert.equal(project.diagnostics.filter((d) => d.code === 'unbound').length, 1);

  const x = symbolsNamed(project, 'x')[0];
  const result = preflightRename(project, x.id, 'z');
  assert.ok(result.ok, JSON.stringify(result.conflicts));
  assert.equal(result.edits.length, 1);
  assert.equal(src.slice(result.edits[0].start, result.edits[0].end), 'x');

  const applied = applyRenamePatch(project, result.patch!);
  assert.ok(applied.ok);
  const after = applied.project;
  assert.equal(after.diagnostics.filter((d) => d.code === 'unbound').length, 1);
  const newSrc = applied.sources.get('a.ml')!;
  assert.match(newSrc, /fn g\(\) \{ return x; \}/);
  assert.match(newSrc, /let z = 1;/);
});

test('sequential order still protects earlier closures from later renames', () => {
  const src = ['fn g() { return x; }', 'let w = 1;', ''].join('\n');
  const project = analyzeProject({ 'a.ml': src });
  const w = symbolsNamed(project, 'w')[0];
  const result = preflightRename(project, w.id, 'x');
  assert.ok(result.ok, JSON.stringify(result.conflicts));
});

test('captured-unbound conflict: rename would bind a previously unbound closure reference', () => {
  const src = ['let w = 1;', 'fn g() { return x; }', ''].join('\n');
  const project = analyzeProject({ 'a.ml': src });
  const w = symbolsNamed(project, 'w')[0];
  const result = preflightRename(project, w.id, 'x');
  assert.equal(result.ok, false);
  assert.equal(result.conflicts.length, 1);
  const conflict = result.conflicts[0];
  assert.equal(conflict.reason, 'captured-unbound');
  assert.equal(src.slice(conflict.start, conflict.end), 'x');
});

test('multi-file: renaming an export updates import specifiers across files', () => {
  const files = {
    'a.ml': 'export let foo = 1;\nlet local = foo;\n',
    'b.ml': 'import { foo as bar } from "./a.ml";\nlet t = bar;\n',
    'c.ml': 'import { foo } from "./a.ml";\nlet u = foo;\n',
  };
  const project = analyzeProject(files);
  assert.deepEqual(project.diagnostics, []);

  const foo = symbolsNamed(project, 'foo', 'a.ml')[0];
  const result = preflightRename(project, foo.id, 'baz');
  assert.ok(result.ok, JSON.stringify(result.conflicts));
  // a.ml: decl + local ref; b.ml: imported name only; c.ml: imported name + tracked local ref.
  assert.equal(result.edits.length, 5);
  const bEdits = result.edits.filter((e) => e.file === 'b.ml');
  assert.equal(bEdits.length, 1);
  assert.equal(files['b.ml'].slice(bEdits[0].start, bEdits[0].end), 'foo');
  const cEdits = result.edits.filter((e) => e.file === 'c.ml');
  assert.equal(cEdits.length, 2);

  const applied = applyRenamePatch(project, result.patch!);
  assert.ok(applied.ok);
  assert.match(applied.sources.get('a.ml')!, /export let baz = 1;/);
  assert.match(applied.sources.get('b.ml')!, /import \{ baz as bar \} from "\.\/a\.ml";/);
  assert.match(applied.sources.get('b.ml')!, /let t = bar;/);
  assert.match(applied.sources.get('c.ml')!, /import \{ baz \} from "\.\/a\.ml";/);
  assert.match(applied.sources.get('c.ml')!, /let u = baz;/);

  // Round-trip on the new project: rename baz back to foo.
  const baz = symbolsNamed(applied.project, 'baz', 'a.ml')[0];
  const back = preflightRename(applied.project, baz.id, 'foo');
  assert.ok(back.ok, JSON.stringify(back.conflicts));
  const reverted = applyRenamePatch(applied.project, back.patch!);
  assert.ok(reverted.ok);
  assert.equal(reverted.sources.get('a.ml'), files['a.ml']);
  assert.equal(reverted.sources.get('b.ml'), files['b.ml']);
  assert.equal(reverted.sources.get('c.ml'), files['c.ml']);
});

test('multi-file: renaming an import alias stays local to the importing file', () => {
  const files = {
    'a.ml': 'export let foo = 1;',
    'b.ml': 'import { foo as bar } from "./a.ml";\nlet t = bar;',
  };
  const project = analyzeProject(files);
  const bar = symbolsNamed(project, 'bar', 'b.ml')[0];
  const result = preflightRename(project, bar.id, 'qux');
  assert.ok(result.ok, JSON.stringify(result.conflicts));
  assert.equal(result.edits.length, 2);
  assert.ok(result.edits.every((e) => e.file === 'b.ml'));

  const applied = applyRenamePatch(project, result.patch!);
  assert.ok(applied.ok);
  assert.match(applied.sources.get('b.ml')!, /import \{ foo as qux \} from "\.\/a\.ml";/);
  assert.match(applied.sources.get('b.ml')!, /let t = qux;/);
  assert.equal(applied.sources.get('a.ml'), files['a.ml']);
});

test('multi-file conflict: export rename collides with an existing top-level binding', () => {
  const files = {
    'a.ml': 'export let foo = 1;',
    'b.ml': 'let baz = 0;\nimport { foo } from "./a.ml";\nlet t = foo;',
  };
  const project = analyzeProject(files);
  const foo = symbolsNamed(project, 'foo', 'a.ml')[0];
  const result = preflightRename(project, foo.id, 'baz');
  assert.equal(result.ok, false);
  assert.ok(result.conflicts.some((c) => c.file === 'b.ml' && c.reason === 'duplicate'));
});

test('renaming the local binding of a non-aliased import reports a conflict', () => {
  const files = {
    'a.ml': 'export let foo = 1;',
    'b.ml': 'import { foo } from "./a.ml";\nlet t = foo;',
  };
  const project = analyzeProject(files);
  const localFoo = symbolsNamed(project, 'foo', 'b.ml')[0];
  const result = preflightRename(project, localFoo.id, 'bar');
  assert.equal(result.ok, false);
  assert.ok(
    result.conflicts.some((c) => c.file === 'b.ml' && c.reason === 'import-missing-export'),
  );
});

test('patch is bound to the source version: stale patches are rejected', () => {
  const project = analyzeProject({ 'a.ml': 'let x = 1;' });
  const x = symbolsNamed(project, 'x')[0];
  const result = preflightRename(project, x.id, 'y');
  assert.ok(result.ok);

  const changed = analyzeProject({ 'a.ml': 'let x = 2;' });
  const applied = applyRenamePatch(changed, result.patch!);
  assert.equal(applied.ok, false);
  if (!applied.ok) assert.match(applied.error, /stale patch/);
});

test('apply re-parses and re-verifies; version changes after a successful apply', () => {
  const project = analyzeProject({ 'a.ml': 'let x = 1;\nlet y = x;' });
  const x = symbolsNamed(project, 'x')[0];
  const result = preflightRename(project, x.id, 'renamed');
  assert.ok(result.ok);
  const applied = applyRenamePatch(project, result.patch!);
  assert.ok(applied.ok);
  assert.notEqual(applied.version, project.version);
  assert.deepEqual(applied.project.diagnostics, []);
  const fa = applied.project.files.get('a.ml')!;
  const renamed = symbolsNamed(applied.project, 'renamed')[0];
  assert.deepEqual([...fa.bindings.values()], [renamed.id]);
});

test('invalid new names are rejected before planning', () => {
  const project = analyzeProject({ 'a.ml': 'let x = 1;' });
  const x = symbolsNamed(project, 'x')[0];
  for (const bad of ['let', '1abc', 'not a name', '']) {
    const result = preflightRename(project, x.id, bad);
    assert.equal(result.ok, false);
    assert.match(result.error!, /invalid identifier/);
  }
  const missing = preflightRename(project, 'a.ml#999', 'ok');
  assert.equal(missing.ok, false);
  assert.match(missing.error!, /symbol not found/);
});
