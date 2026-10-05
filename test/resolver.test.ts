import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Project, type Resolution } from '../src/index.ts';
import { validateSnapshot } from '../src/validate.ts';

const chainOf = (r: Resolution): string[] => {
  assert.equal(r.status, 'resolved');
  return (r as Extract<Resolution, { status: 'resolved' }>).chain.map((s) => `${s.module}#${s.name}`);
};

test('named exports and explicit imports resolve to the defining module', () => {
  const project = new Project();
  project.setModule('/a.ts', { definitions: ['foo', 'bar'] });
  const snap = project.setModule('/b.ts', { imports: [{ name: 'foo', from: '/a.ts' }] });

  const r = snap.resolveImport('/b.ts', 'foo');
  assert.equal(r.status, 'resolved');
  assert.deepEqual(chainOf(r), ['/a.ts#foo']);
  assert.deepEqual(snap.diagnostics('/b.ts'), []);
  assert.deepEqual(validateSnapshot(snap), []);
});

test('re-export chains with renames resolve to the original definition', () => {
  const project = new Project();
  project.setModule('/d.ts', { definitions: ['a'] });
  project.setModule('/c.ts', { reexports: [{ name: 'a', from: '/d.ts', as: 'b' }] });
  project.setModule('/b.ts', { reexports: [{ name: 'b', from: '/c.ts', as: 'c' }] });
  const snap = project.setModule('/a.ts', { imports: [{ name: 'c', from: '/b.ts' }] });

  const r = snap.resolveImport('/a.ts', 'c');
  assert.equal(r.status, 'resolved');
  if (r.status === 'resolved') {
    assert.deepEqual(r.origin, { module: '/d.ts', name: 'a' });
  }
  assert.deepEqual(chainOf(r), ['/b.ts#c', '/c.ts#b', '/d.ts#a']);
  assert.deepEqual(validateSnapshot(snap), []);
});

test('diamond re-exports converge on one definition without conflict', () => {
  const project = new Project();
  project.setModule('/d.ts', { definitions: ['x'] });
  project.setModule('/b.ts', { reexports: [{ name: 'x', from: '/d.ts' }] });
  project.setModule('/c.ts', { reexports: [{ name: 'x', from: '/d.ts' }] });
  const snap = project.setModule('/a.ts', {
    reexports: [
      { name: 'x', from: '/b.ts' },
      { name: 'x', from: '/c.ts' },
    ],
  });

  const r = snap.resolveExport('/a.ts', 'x');
  assert.equal(r.status, 'resolved');
  if (r.status === 'resolved') {
    assert.deepEqual(r.origin, { module: '/d.ts', name: 'x' });
    assert.equal(r.chain.length, 3);
  }
  assert.deepEqual(snap.diagnostics('/a.ts'), []);
  assert.deepEqual(validateSnapshot(snap), []);
});

test('conflicting names report the two shortest source chains', () => {
  const project = new Project();
  project.setModule('/b.ts', { definitions: ['x'] }); // depth 1 from /a.ts
  project.setModule('/d.ts', { definitions: ['x'] });
  project.setModule('/c.ts', { reexports: [{ name: 'x', from: '/d.ts' }] }); // depth 2
  project.setModule('/g.ts', { definitions: ['x'] });
  project.setModule('/f.ts', { reexports: [{ name: 'x', from: '/g.ts' }] });
  project.setModule('/e.ts', { reexports: [{ name: 'x', from: '/f.ts' }] }); // depth 3
  const snap = project.setModule('/a.ts', {
    reexports: [
      { name: 'x', from: '/e.ts' },
      { name: 'x', from: '/b.ts' },
      { name: 'x', from: '/c.ts' },
    ],
  });

  const r = snap.resolveExport('/a.ts', 'x');
  assert.equal(r.status, 'conflict');
  if (r.status === 'conflict') {
    const [first, second] = r.definitions;
    assert.deepEqual(first.origin, { module: '/b.ts', name: 'x' });
    assert.deepEqual(first.chain.map((s) => s.module), ['/a.ts', '/b.ts']);
    assert.deepEqual(second.origin, { module: '/d.ts', name: 'x' });
    assert.deepEqual(second.chain.map((s) => s.module), ['/a.ts', '/c.ts', '/d.ts']);
  }
  const diags = snap.diagnostics('/a.ts');
  assert.equal(diags.length, 1);
  assert.equal(diags[0]!.kind, 'conflicting-export');
  assert.match(diags[0]!.message, /chain 1/);
  assert.match(diags[0]!.message, /chain 2/);
  assert.deepEqual(validateSnapshot(snap), []);
});

test('circular re-exports without any definition are rejected', () => {
  const project = new Project();
  project.setModule('/a.ts', { reexports: [{ name: 'x', from: '/b.ts' }] });
  const snap = project.setModule('/b.ts', { reexports: [{ name: 'x', from: '/a.ts' }] });

  const r = snap.resolveExport('/a.ts', 'x');
  assert.equal(r.status, 'circular');
  if (r.status === 'circular') {
    assert.ok(r.cycle.length >= 2, 'cycle witness is reported');
    assert.deepEqual(r.cycle[0], r.cycle[r.cycle.length - 1]);
  }
  assert.equal(snap.resolveExport('/b.ts', 'x').status, 'circular');
  assert.ok(snap.diagnostics('/a.ts').some((d) => d.kind === 'circular-reexport'));
  assert.deepEqual(validateSnapshot(snap), []);
});

test('circular re-exports are fine when a definition is reachable', () => {
  const project = new Project();
  project.setModule('/c.ts', { definitions: ['x'] });
  project.setModule('/a.ts', { reexports: [{ name: 'x', from: '/b.ts' }] });
  const snap = project.setModule('/b.ts', {
    reexports: [
      { name: 'x', from: '/a.ts' },
      { name: 'x', from: '/c.ts' },
    ],
  });

  const r = snap.resolveExport('/a.ts', 'x');
  assert.equal(r.status, 'resolved');
  if (r.status === 'resolved') {
    assert.deepEqual(r.origin, { module: '/c.ts', name: 'x' });
  }
  assert.deepEqual(validateSnapshot(snap), []);
});

test('circular imports are allowed', () => {
  const project = new Project();
  project.setModule('/a.ts', { definitions: ['a'], imports: [{ name: 'b', from: '/b.ts' }] });
  const snap = project.setModule('/b.ts', { definitions: ['b'], imports: [{ name: 'a', from: '/a.ts' }] });

  assert.equal(snap.resolveImport('/a.ts', 'b').status, 'resolved');
  assert.equal(snap.resolveImport('/b.ts', 'a').status, 'resolved');
  assert.deepEqual(snap.diagnostics('/a.ts'), []);
  assert.deepEqual(snap.diagnostics('/b.ts'), []);
  assert.deepEqual(validateSnapshot(snap), []);
});

test('single-module update invalidates only affected symbols; old snapshots stay queryable', () => {
  const project = new Project();
  project.setModule('/d.ts', { definitions: ['x'] });
  project.setModule('/c.ts', { reexports: [{ name: 'x', from: '/d.ts' }] });
  project.setModule('/b.ts', { reexports: [{ name: 'x', from: '/c.ts' }] });
  project.setModule('/a.ts', { imports: [{ name: 'x', from: '/b.ts' }] });
  project.setModule('/u.ts', { definitions: ['u'] });
  const s1 = project.setModule('/v.ts', { imports: [{ name: 'u', from: '/u.ts' }] });

  assert.equal(s1.resolveImport('/a.ts', 'x').status, 'resolved');
  assert.equal(s1.resolveImport('/v.ts', 'u').status, 'resolved');
  assert.equal(s1.resolveExport('/c.ts', 'x').status, 'resolved');
  assert.equal(s1.resolveExport('/d.ts', 'x').status, 'resolved');
  assert.equal(s1.stats.computed, 4);

  // The dependency cone of /d.ts covers the x chain, but not the u chain.
  assert.deepEqual(project.affectedSymbols('/d.ts'), ['/b.ts x', '/c.ts x', '/d.ts x'].sort());
  assert.deepEqual(project.affectedSymbols('/u.ts'), ['/u.ts u']);

  // Deleting the final definition of x.
  const s2 = project.setModule('/d.ts', {});

  // Unaffected symbol is served from the inherited cache (no recomputation).
  const before = s2.stats.computed;
  const u = s2.resolveImport('/v.ts', 'u');
  assert.equal(u.status, 'resolved');
  assert.equal(s2.stats.computed, before);
  assert.ok(s2.stats.reused >= 1);

  // Affected symbols are recomputed and now unresolved.
  assert.equal(s2.resolveImport('/a.ts', 'x').status, 'unresolved');
  assert.equal(s2.resolveExport('/c.ts', 'x').status, 'unresolved');
  assert.ok(s2.diagnostics('/b.ts').some((d) => d.kind === 'unresolved-reexport'));

  // The old snapshot still answers with the pre-update state.
  const old = s1.resolveImport('/a.ts', 'x');
  assert.equal(old.status, 'resolved');
  if (old.status === 'resolved') {
    assert.deepEqual(old.origin, { module: '/d.ts', name: 'x' });
  }
  assert.equal(s1.version + 1, s2.version);

  assert.deepEqual(validateSnapshot(s1), []);
  assert.deepEqual(validateSnapshot(s2), []);
});

test('removing a module updates dependents; re-adding restores them', () => {
  const project = new Project();
  project.setModule('/d.ts', { definitions: ['x'] });
  const s1 = project.setModule('/a.ts', { reexports: [{ name: 'x', from: '/d.ts' }] });
  assert.equal(s1.resolveExport('/a.ts', 'x').status, 'resolved');

  const s2 = project.removeModule('/d.ts');
  assert.equal(s2.resolveExport('/a.ts', 'x').status, 'unresolved');

  const s3 = project.setModule('/d.ts', { definitions: ['x'] });
  assert.equal(s3.resolveExport('/a.ts', 'x').status, 'resolved');

  // Old snapshots remain frozen in time.
  assert.equal(s1.resolveExport('/a.ts', 'x').status, 'resolved');
  assert.equal(s2.resolveExport('/a.ts', 'x').status, 'unresolved');
  assert.deepEqual(validateSnapshot(s3), []);
});

test('rename then deletion of the final definition surfaces through the whole chain', () => {
  const project = new Project();
  project.setModule('/d.ts', { definitions: ['a'] });
  project.setModule('/c.ts', { reexports: [{ name: 'a', from: '/d.ts', as: 'b' }] });
  const s1 = project.setModule('/b.ts', { imports: [{ name: 'b', from: '/c.ts' }] });

  const ok = s1.resolveImport('/b.ts', 'b');
  assert.equal(ok.status, 'resolved');
  if (ok.status === 'resolved') {
    assert.deepEqual(ok.origin, { module: '/d.ts', name: 'a' });
  }

  const s2 = project.setModule('/d.ts', { definitions: ['other'] });
  assert.equal(s2.resolveImport('/b.ts', 'b').status, 'unresolved');
  assert.ok(s2.diagnostics('/c.ts').some((d) => d.kind === 'unresolved-reexport'));
  assert.ok(s2.diagnostics('/b.ts').some((d) => d.kind === 'unresolved-import'));

  // Old snapshot still resolves through the renamed chain.
  assert.equal(s1.resolveImport('/b.ts', 'b').status, 'resolved');
  assert.deepEqual(validateSnapshot(s1), []);
  assert.deepEqual(validateSnapshot(s2), []);
});
