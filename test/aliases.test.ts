import test from 'node:test';
import assert from 'node:assert/strict';
import { VfsStore, ERR } from '../src/index.ts';
import { mkpath, hasCode } from './util.ts';

test('longest alias prefix wins', () => {
  const store = new VfsStore();
  const n1 = mkpath(store, '/r1');
  const n1c = mkpath(store, '/r1/c');
  const n2 = mkpath(store, '/r2');
  const n2c = mkpath(store, '/r2/c');
  store.setAlias('/p', n1);
  store.setAlias('/p/q', n2);
  assert.equal(store.resolvePath('/p/c').nodeId, n1c);
  assert.equal(store.resolvePath('/p/q/c').nodeId, n2c);
});

test('nested aliases consume bounded jumps', () => {
  const store = new VfsStore();
  const actual = mkpath(store, '/actual');
  const actualBase = mkpath(store, '/actual/base');
  const hidden = mkpath(store, '/real/base');
  store.setAlias('/real', actual);
  store.setAlias('/a', hidden);
  // /a -> /real/base -> /actual/base: two jumps through nested aliases
  const resolved = store.resolvePath('/a');
  assert.equal(resolved.nodeId, actualBase);
  assert.equal(resolved.hops, 2);
  assert.throws(() => store.resolvePath('/a', 1), hasCode(ERR.AliasResolutionLimit));
});

test('deleted alias target fails resolution cleanly', () => {
  const store = new VfsStore();
  const n = mkpath(store, '/doomed');
  store.setAlias('/d', n);
  assert.equal(store.resolvePath('/d').nodeId, n);
  store.tree.remove(n);
  assert.throws(() => store.resolvePath('/d'), hasCode(ERR.AliasTargetMissing));
});

test('same-prefix alias conflicts, overlapping prefixes coexist', () => {
  const store = new VfsStore();
  const a = mkpath(store, '/a-real');
  const b = mkpath(store, '/b-real');
  store.setAlias('/x/y', a);
  assert.throws(() => store.setAlias('/x/y', b), hasCode(ERR.AliasConflict));
  store.setAlias('/x', b);
  assert.equal(store.aliases.targetOf('/x/y'), a);
  assert.equal(store.aliases.targetOf('/x'), b);
});

test('alias cycles are rejected at configuration time', () => {
  const store = new VfsStore();
  const x = mkpath(store, '/p');
  const y = mkpath(store, '/q');
  store.setAlias('/p', y);
  // /q -> x (real path /p) -> alias /p -> y (real path /q) -> alias /q ...
  assert.throws(() => store.setAlias('/q', x), hasCode(ERR.AliasCycle));
  assert.equal(store.aliases.has('/q'), false);
});

test('self-shadowing alias is rejected as a cycle', () => {
  const store = new VfsStore();
  const inner = mkpath(store, '/a/b');
  // /a -> /a/b -> alias /a -> /a/b/b -> ... never terminates
  assert.throws(() => store.setAlias('/a', inner), hasCode(ERR.AliasCycle));
  assert.equal(store.aliases.has('/a'), false);
});

test('moving a node updates its real path but keeps alias target ids', () => {
  const store = new VfsStore();
  const n = mkpath(store, '/real/n');
  mkpath(store, '/other');
  store.setAlias('/link', n);
  store.movePath('/real/n', '/other/n');
  assert.equal(store.aliases.targetOf('/link'), n);
  assert.equal(store.tree.realPath(n), '/other/n');
  assert.equal(store.resolvePath('/link').nodeId, n);
});
