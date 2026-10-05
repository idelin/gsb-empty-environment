import { test } from 'node:test';
import assert from 'node:assert/strict';
import { analyzeProject, type Project } from '../src/index.ts';

function symbolsNamed(project: Project, name: string, file?: string) {
  return [...project.symbols.values()].filter(
    (s) => s.name === name && (file === undefined || s.file === file),
  );
}

test('sequential declarations: reference before declaration is unbound', () => {
  const src = 'let a = b;\nlet b = 1;\nlet c = b;';
  const project = analyzeProject({ 'a.ml': src });
  const unbound = project.diagnostics.filter((d) => d.code === 'unbound');
  assert.equal(unbound.length, 1);
  assert.equal(src.slice(unbound[0].start, unbound[0].end), 'b');

  const fa = project.files.get('a.ml')!;
  const bSym = symbolsNamed(project, 'b')[0];
  const boundRefs = [...fa.bindings.values()].filter((id) => id === bSym.id);
  assert.equal(boundRefs.length, 1);
});

test('closures capture only declarations visible at the definition site', () => {
  const src = [
    'let x = 1;',
    'fn g() { return x; }',
    'let y = 2;',
    'fn h() { return y; }',
    'fn k() { return z; }',
    'let z = 3;',
  ].join('\n');
  const project = analyzeProject({ 'a.ml': src });
  const unbound = project.diagnostics.filter((d) => d.code === 'unbound');
  assert.equal(unbound.length, 1);
  assert.equal(src.slice(unbound[0].start, unbound[0].end), 'z');
});

test('block scope: inner declarations do not leak, inner shadows outer', () => {
  const src = 'let x = 1;\n{ let x = 2; let inner = x; }\nlet y = x;\nlet leak = inner;';
  const project = analyzeProject({ 'a.ml': src });
  const unbound = project.diagnostics.filter((d) => d.code === 'unbound');
  assert.equal(unbound.length, 1);
  assert.equal(src.slice(unbound[0].start, unbound[0].end), 'inner');

  const fa = project.files.get('a.ml')!;
  const [outerX, innerX] = symbolsNamed(project, 'x');
  const innerDecl = symbolsNamed(project, 'inner')[0];
  const yDecl = symbolsNamed(project, 'y')[0];
  // `let inner = x` refers to the block-local x; `let y = x` refers to the outer x.
  const innerInitIdent = fa.program.idents.find(
    (id) => id.start > innerDecl.nameIdent.end && id.name === 'x',
  )!;
  assert.equal(fa.bindings.get(innerInitIdent.id), innerX.id);
  const yInitIdent = fa.program.idents.find(
    (id) => id.start > yDecl.nameIdent.end && id.name === 'x',
  )!;
  assert.equal(fa.bindings.get(yInitIdent.id), outerX.id);
});

test('recursion: fn body sees its own name', () => {
  const project = analyzeProject({ 'a.ml': 'fn fact(n) { return fact(n); }' });
  assert.deepEqual(project.diagnostics, []);
});

test('duplicate declaration in the same scope is reported', () => {
  const project = analyzeProject({ 'a.ml': 'let x = 1;\nlet x = 2;' });
  assert.equal(project.diagnostics.filter((d) => d.code === 'duplicate').length, 1);
});

test('imports: alias binds locally, missing module and missing export are diagnostics', () => {
  const project = analyzeProject({
    'a.ml': 'export let foo = 1;',
    'b.ml': 'import { foo as bar } from "./a.ml";\nlet t = bar;',
    'c.ml': 'import { nope } from "./a.ml";\nimport { foo } from "./missing.ml";',
  });
  const codes = project.diagnostics.map((d) => d.code).sort();
  assert.deepEqual(codes, ['import-missing-export', 'import-unresolved']);

  const bFa = project.files.get('b.ml')!;
  const bar = symbolsNamed(project, 'bar', 'b.ml')[0];
  const foo = symbolsNamed(project, 'foo', 'a.ml')[0];
  assert.equal(bar.target, foo.id);
  const barRefs = [...bFa.bindings.values()].filter((id) => id === bar.id);
  assert.equal(barRefs.length, 1);
});
