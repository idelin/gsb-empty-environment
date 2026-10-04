import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  planRename,
  applyPatch,
  bindProject,
  type Conflict,
  type Patch,
} from '../src/index.ts';

type Files = Record<string, string>;

function declId(files: Files, file: string, name: string, nth = 0): string {
  const bound = bindProject(new Map(Object.entries(files)));
  const matches = [...bound.decls.values()].filter((d) => d.file === file && d.name === name);
  assert.ok(matches.length > nth, `decl ${name}#${nth} not found in ${file}`);
  return matches[nth].id;
}

function planOk(files: Files, symbolId: string, newName: string): Patch {
  const r = planRename(files, symbolId, newName);
  assert.ok(r.ok, `expected plan to succeed, got ${JSON.stringify(r)}`);
  return r.patch;
}

function planConflict(files: Files, symbolId: string, newName: string): Conflict[] {
  const r = planRename(files, symbolId, newName);
  assert.ok(!r.ok, `expected plan to fail with conflicts`);
  assert.ok(r.conflicts.length > 0, 'expected at least one conflict');
  return r.conflicts;
}

function applyOk(files: Files, patch: Patch): Map<string, string> {
  const r = applyPatch(files, patch);
  assert.ok(r.ok, `expected apply to succeed, got ${JSON.stringify(r)}`);
  return r.sources;
}

test('block-scoped rename updates declaration and all references', () => {
  const files: Files = {
    'main.lang': `let x = 1;
{
  let y = x + 1;
  print(y);
}
print(x);
`,
  };
  const patch = planOk(files, declId(files, 'main.lang', 'x'), 'q');
  const out = applyOk(files, patch);
  assert.equal(
    out.get('main.lang'),
    `let q = 1;
{
  let y = q + 1;
  print(y);
}
print(q);
`,
  );
});

test('closure with same-name variable: renaming outer leaves inner intact', () => {
  const files: Files = {
    'main.lang': `let x = 1;
fn f() {
  let x = 2;
  return x;
}
print(x);
`,
  };
  const patch = planOk(files, declId(files, 'main.lang', 'x', 0), 'y');
  const out = applyOk(files, patch);
  assert.equal(
    out.get('main.lang'),
    `let y = 1;
fn f() {
  let x = 2;
  return x;
}
print(y);
`,
  );
});

test('closure with same-name variable: renaming inner leaves outer intact', () => {
  const files: Files = {
    'main.lang': `let x = 1;
fn f() {
  let x = 2;
  return x;
}
print(x);
`,
  };
  const patch = planOk(files, declId(files, 'main.lang', 'x', 1), 'y');
  const out = applyOk(files, patch);
  assert.equal(
    out.get('main.lang'),
    `let x = 1;
fn f() {
  let y = 2;
  return y;
}
print(x);
`,
  );
});

test('conflict: rename would capture a closure reference to an outer variable', () => {
  const files: Files = {
    'main.lang': `let y = 1;
fn f() {
  let x = 2;
  return y + x;
}
`,
  };
  const conflicts = planConflict(files, declId(files, 'main.lang', 'x'), 'y');
  assert.equal(conflicts.length, 1);
  assert.equal(conflicts[0].kind, 'captured');
  assert.equal(conflicts[0].line, 4);
  assert.equal(conflicts[0].name, 'y');
});

test('conflict: renamed reference would be shadowed by an inner declaration', () => {
  const files: Files = {
    'main.lang': `let y = 1;
fn f() {
  let x = 2;
  return y;
}
`,
  };
  const conflicts = planConflict(files, declId(files, 'main.lang', 'y'), 'x');
  assert.equal(conflicts.length, 1);
  assert.equal(conflicts[0].kind, 'shadowed');
  assert.equal(conflicts[0].line, 4);
});

test('use before declaration binds to the outer symbol and renames with it', () => {
  const files: Files = {
    'main.lang': `let a = 1;
{
  print(a);
  let a = 2;
  print(a);
}
`,
  };
  const patch = planOk(files, declId(files, 'main.lang', 'a', 0), 'c');
  const out = applyOk(files, patch);
  assert.equal(
    out.get('main.lang'),
    `let c = 1;
{
  print(c);
  let a = 2;
  print(a);
}
`,
  );
});

test('use before declaration: renaming the inner symbol only touches later references', () => {
  const files: Files = {
    'main.lang': `let a = 1;
{
  print(a);
  let a = 2;
  print(a);
}
`,
  };
  const patch = planOk(files, declId(files, 'main.lang', 'a', 1), 'c');
  const out = applyOk(files, patch);
  assert.equal(
    out.get('main.lang'),
    `let a = 1;
{
  print(a);
  let c = 2;
  print(c);
}
`,
  );
});

test('conflict: sequential self-capture in "let b = a" when renaming a to b', () => {
  const files: Files = {
    'main.lang': `let a = 1;
let b = a;
`,
  };
  const conflicts = planConflict(files, declId(files, 'main.lang', 'a'), 'b');
  assert.equal(conflicts.length, 1);
  assert.equal(conflicts[0].kind, 'shadowed');
  assert.equal(conflicts[0].line, 2);
});

test('sequential: a later same-name declaration does not steal earlier references', () => {
  const files: Files = {
    'main.lang': `let a = 1;
print(a);
let b = 2;
`,
  };
  const patch = planOk(files, declId(files, 'main.lang', 'a'), 'b');
  const out = applyOk(files, patch);
  assert.equal(
    out.get('main.lang'),
    `let b = 1;
print(b);
let b = 2;
`,
  );
});

const multi: Files = {
  'other.lang': `let x = 1;
let y = 2;
`,
  'main.lang': `import { x as a, y } from "./other.lang";
print(a);
print(y);
`,
};

test('multi-file: renaming an export updates the imported name but keeps the alias', () => {
  const patch = planOk(multi, declId(multi, 'other.lang', 'x'), 'z');
  const out = applyOk(multi, patch);
  assert.equal(out.get('other.lang'), `let z = 1;\nlet y = 2;\n`);
  assert.equal(
    out.get('main.lang'),
    `import { z as a, y } from "./other.lang";
print(a);
print(y);
`,
  );
});

test('multi-file: renaming a shorthand-imported export expands to "new as old"', () => {
  const patch = planOk(multi, declId(multi, 'other.lang', 'y'), 'z');
  const out = applyOk(multi, patch);
  assert.equal(
    out.get('main.lang'),
    `import { x as a, z as y } from "./other.lang";
print(a);
print(y);
`,
  );
});

test('multi-file: renaming a local alias only touches the importing file', () => {
  const patch = planOk(multi, declId(multi, 'main.lang', 'a'), 'b');
  const out = applyOk(multi, patch);
  assert.equal(out.get('other.lang'), multi['other.lang']);
  assert.equal(
    out.get('main.lang'),
    `import { x as b, y } from "./other.lang";
print(b);
print(y);
`,
  );
});

test('multi-file: renaming a shorthand alias keeps importing the original name', () => {
  const patch = planOk(multi, declId(multi, 'main.lang', 'y'), 'w');
  const out = applyOk(multi, patch);
  assert.equal(
    out.get('main.lang'),
    `import { x as a, y as w } from "./other.lang";
print(a);
print(w);
`,
  );
});

test('multi-file conflict: renaming an alias collides with a local declaration', () => {
  const files: Files = {
    'other.lang': `let x = 1;\n`,
    'main.lang': `import { x as a } from "./other.lang";
let z = 3;
print(a);
`,
  };
  const conflicts = planConflict(files, declId(files, 'main.lang', 'a'), 'z');
  assert.equal(conflicts.length, 1);
  assert.equal(conflicts[0].kind, 'shadowed');
  assert.equal(conflicts[0].line, 3);
});

test('patch is bound to the source version and refuses stale sources', () => {
  const files: Files = { 'main.lang': `let x = 1;\nprint(x);\n` };
  const patch = planOk(files, declId(files, 'main.lang', 'x'), 'y');
  const tampered: Files = { 'main.lang': `let x = 2;\nprint(x);\n` };
  const r = applyPatch(tampered, patch);
  assert.ok(!r.ok);
  assert.match(r.error ?? '', /version/);
});

test('applying a patch re-parses and re-verifies bindings', () => {
  const files: Files = {
    'main.lang': `let x = 1;
fn f() {
  return x;
}
print(x);
`,
  };
  const patch = planOk(files, declId(files, 'main.lang', 'x'), 'value');
  const r = applyPatch(files, patch);
  assert.ok(r.ok);
  assert.deepEqual(r.conflicts, []);
  const rebound = bindProject(r.sources);
  const decl = rebound.decls.get(patch.symbolId.replace(/:\d+$/, `:${r.sources.get('main.lang')!.indexOf('value')}`));
  assert.ok(decl);
  assert.equal(decl.name, 'value');
  const refs = rebound.refs.filter((ref) => ref.target === decl.id);
  assert.equal(refs.length, 2);
});

test('invalid new names are rejected', () => {
  const files: Files = { 'main.lang': `let x = 1;\n` };
  const id = declId(files, 'main.lang', 'x');
  for (const bad of ['let', '1abc', 'a-b', '']) {
    const r = planRename(files, id, bad);
    assert.ok(!r.ok, `expected rejection of ${JSON.stringify(bad)}`);
    assert.ok(r.error);
  }
});

test('unknown symbol id is rejected', () => {
  const files: Files = { 'main.lang': `let x = 1;\n` };
  const r = planRename(files, 'main.lang:999', 'y');
  assert.ok(!r.ok);
  assert.match(r.error ?? '', /unknown symbol/);
});
