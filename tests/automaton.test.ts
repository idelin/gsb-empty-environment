import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Automaton } from '../src/automaton.ts';
import { parsePattern, patternMatchesPath } from '../src/pattern.ts';
import { parsePath } from '../src/path.ts';

test('shared prefixes compile to shared states', () => {
  const a = new Automaton();
  a.add(1, parsePattern('/a/b/c'));
  a.add(2, parsePattern('/a/b/d'));
  // root + a + b + c + d
  assert.equal(a.stateCount, 5);
  a.add(3, parsePattern('/a/b/c')); // duplicate pattern shares everything
  assert.equal(a.stateCount, 5);
});

test('removal prunes only unshared states', () => {
  const a = new Automaton();
  a.add(1, parsePattern('/a/b/c'));
  a.add(2, parsePattern('/a/b/d'));
  assert.ok(a.remove(1, parsePattern('/a/b/c')));
  // root + a + b + d remain
  assert.equal(a.stateCount, 4);
  assert.deepEqual(a.match(parsePath('/a/b/c')).subs, []);
  assert.deepEqual(a.match(parsePath('/a/b/d')).subs, [2]);
  assert.ok(!a.remove(1, parsePattern('/a/b/c')));
});

test('ancestor semantics: parent pattern matches descendant paths', () => {
  const a = new Automaton();
  a.add(1, parsePattern('/users'));
  assert.deepEqual(a.match(parsePath('/users')).subs, [1]);
  assert.deepEqual(a.match(parsePath('/users/u1/name')).subs, [1]);
  assert.deepEqual(a.match(parsePath('/other')).subs, []);
});

test("'*' matches exactly one segment, '**' matches any depth", () => {
  const a = new Automaton();
  a.add(1, parsePattern('/users/*/name'));
  a.add(2, parsePattern('/logs/**'));
  assert.deepEqual(a.match(parsePath('/users/u1/name')).subs.sort(), [1]);
  // ancestor semantics extend through the wildcard
  assert.deepEqual(a.match(parsePath('/users/u1/name/first')).subs.sort(), [1]);
  assert.deepEqual(a.match(parsePath('/users/u1/email')).subs, []);
  assert.deepEqual(a.match(parsePath('/logs/a/b/c')).subs.sort(), [2]);
  assert.deepEqual(a.match(parsePath('/logs')).subs.sort(), [2]);
});

test('adjacent string prefixes are not ancestors', () => {
  const a = new Automaton();
  a.add(1, parsePattern('/users/ab'));
  assert.deepEqual(a.match(parsePath('/users/abc')).subs, []);
  assert.deepEqual(a.match(parsePath('/users/abc/x')).subs, []);
  assert.deepEqual(a.match(parsePath('/users/ab/x')).subs, [1]);
});

test('automaton matches the naive per-pattern reference on randomized inputs', () => {
  const segments = ['a', 'b', 'ab', 'c', '0', '1'];
  const rand = (() => {
    let seed = 42;
    return () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  })();
  const randSeg = () => segments[Math.floor(rand() * segments.length)];
  const randPatternSeg = () => {
    const r = rand();
    if (r < 0.25) return '*';
    return randSeg();
  };

  const a = new Automaton();
  const patterns = new Map<number, ReturnType<typeof parsePattern>>();
  for (let id = 1; id <= 60; id++) {
    const len = Math.floor(rand() * 5);
    const source = '/' + Array.from({ length: len }, randPatternSeg).join('/');
    const pattern = parsePattern(source === '/' ? '/' : source);
    patterns.set(id, pattern);
    a.add(id, pattern);
  }
  for (let i = 0; i < 300; i++) {
    const len = Math.floor(rand() * 7);
    const path = Array.from({ length: len }, randSeg);
    const expected = [...patterns.entries()]
      .filter(([, p]) => patternMatchesPath(p, path))
      .map(([id]) => id)
      .sort((x, y) => x - y);
    assert.deepEqual(a.match(path).subs.slice().sort((x, y) => x - y), expected);
  }
});

test('long paths with many shared prefixes: visited states stay small', () => {
  const a = new Automaton();
  const prefix = '/env/prod/region-a/cluster-7/service';
  const subCount = 200;
  for (let i = 0; i < subCount; i++) {
    a.add(i + 1, parsePattern(`${prefix}/worker-${i}/config`));
  }
  a.add(1000, parsePattern('/env/prod/*/cluster-7/service/*/config'));

  const path = parsePath(`${prefix}/worker-42/config/logging/level`);
  const result = a.match(path);

  // Naive cost: every subscriber compares its full pattern against the path.
  let naive = 0;
  for (let i = 0; i < subCount; i++) {
    naive += parsePattern(`${prefix}/worker-${i}/` + 'config').length + 1;
  }
  naive += parsePattern('/env/prod/*/cluster-7/service/*/config').length + 1;

  console.log(
    `    [stats] subscribers=${subCount + 1} automatonStates=${a.stateCount} ` +
      `pathDepth=${path.length} visitedStates=${result.visited} naiveComparisons~=${naive}`,
  );
  assert.deepEqual(result.subs.slice().sort((x, y) => x - y), [43, 1000]);
  // Shared prefix is traversed once: visited states are orders of magnitude
  // smaller than per-subscriber matching.
  assert.ok(result.visited < 20, `visited=${result.visited}`);
  assert.ok(naive > subCount * 5);
});
