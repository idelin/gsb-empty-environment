import assert from 'node:assert/strict';
import { test } from 'node:test';
import { SuggestionEngine } from '../src/index.ts';
import type { Candidate, Suggestion } from '../src/index.ts';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

const candidate = (
  id: string,
  key: string,
  score: number,
  explanation?: string,
): Candidate => ({ id, key, score, explanation });

/** Independent reference aggregation used to cross-check the engine. */
function referenceAggregate(
  batches: ReadonlyArray<readonly [string, readonly Candidate[]]>,
): Array<{ key: string; score: number; sources: string[] }> {
  const byKey = new Map<string, Map<string, number>>();
  for (const [sourceId, candidates] of batches) {
    for (const c of candidates) {
      const key = c.key.normalize('NFKC').trim().toLowerCase().replace(/\s+/g, ' ');
      if (key === '') continue;
      let bySource = byKey.get(key);
      if (!bySource) byKey.set(key, (bySource = new Map()));
      bySource.set(sourceId, Math.max(bySource.get(sourceId) ?? -Infinity, c.score));
    }
  }
  return [...byKey]
    .map(([key, bySource]) => ({
      key,
      score: Math.max(...bySource.values()),
      sources: [...bySource.keys()].sort(),
    }))
    .sort((a, b) => b.score - a.score || (a.key < b.key ? -1 : 1));
}

test('dedupes by normalized key, keeps max score and the source set', () => {
  const engine = new SuggestionEngine();
  const gen = engine.beginQuery('hel');

  engine.applyBatch(gen, 'prefix', [
    candidate('p1', '  Hello ', 0.6, 'prefix match'),
    candidate('p2', 'help', 0.4),
  ]);
  engine.applyBatch(gen, 'history', [
    candidate('h1', 'HELLO', 0.9, 'seen 12 times'),
    candidate('h2', 'hello world', 0.7, 'seen 3 times'),
  ]);
  engine.applyBatch(gen, 'fuzzy', [candidate('f1', 'hello', 0.5)]);

  const list = engine.suggestions();
  assert.deepEqual(
    list.map((s) => s.key),
    ['hello', 'hello world', 'help'],
  );

  const hello = list[0];
  assert.equal(hello.score, 0.9, 'winner score is the max across sources');
  assert.deepEqual([...hello.sources].sort(), ['fuzzy', 'history', 'prefix']);
  assert.equal(hello.explanations.get('prefix'), 'prefix match');
  assert.equal(hello.explanations.get('history'), 'seen 12 times');
  assert.equal(hello.explanations.has('fuzzy'), false, 'source without explanation is absent');
});

test('re-selects the winner when a source retracts', () => {
  const engine = new SuggestionEngine();
  const gen = engine.beginQuery('a');

  engine.applyBatch(gen, 'weak', [candidate('w1', 'apple', 0.4, 'weak guess')]);
  engine.applyBatch(gen, 'strong', [
    candidate('s1', 'apple', 0.95, 'strong guess'),
    candidate('s2', 'apricot', 0.8),
  ]);

  let list = engine.suggestions();
  assert.equal(list[0].key, 'apple');
  assert.equal(list[0].score, 0.95);
  assert.deepEqual([...list[0].sources].sort(), ['strong', 'weak']);

  engine.retractSource('strong');
  list = engine.suggestions();
  assert.deepEqual(
    list.map((s) => [s.key, s.score]),
    [['apple', 0.4]],
    'apricot disappears with its only source; apple falls back to the weak score',
  );
  assert.deepEqual([...list[0].sources], ['weak']);
  assert.equal(list[0].explanations.get('weak'), 'weak guess');

  engine.retractSource('weak');
  assert.deepEqual(engine.suggestions(), []);
});

test('a source batch replaces its earlier batch in the same generation', () => {
  const engine = new SuggestionEngine();
  const gen = engine.beginQuery('x');
  engine.applyBatch(gen, 's', [candidate('1', 'alpha', 0.3)]);
  engine.applyBatch(gen, 's', [candidate('2', 'beta', 0.7)]);
  assert.deepEqual(
    engine.suggestions().map((s) => s.key),
    ['beta'],
    'stale contribution from the same source is replaced, not merged',
  );
});

test('keeps highlight identity across re-sorts and retractions', () => {
  const engine = new SuggestionEngine();
  const gen = engine.beginQuery('q');
  engine.applyBatch(gen, 'a', [
    candidate('a1', 'alpha', 0.5),
    candidate('a2', 'beta', 0.9),
  ]);

  assert.equal(engine.highlight(' ALPHA ')!.key, 'alpha', 'highlight accepts raw keys');

  // A new source reorders the list; the highlight must follow the key, not the index.
  engine.applyBatch(gen, 'b', [candidate('b1', 'gamma', 0.99)]);
  assert.deepEqual(
    engine.suggestions().map((s) => s.key),
    ['gamma', 'beta', 'alpha'],
  );
  assert.equal(engine.highlighted()!.key, 'alpha');

  // Unrelated retraction: alpha survives and keeps its object identity.
  const before = engine.suggestions().find((s) => s.key === 'alpha')!;
  engine.retractSource('b');
  const after = engine.highlighted();
  assert.equal(after!.key, 'alpha');
  assert.strictEqual(after, before, 'unchanged key keeps the same object reference');

  // The contributing source retracts: highlight is absent but remembered.
  engine.retractSource('a');
  assert.equal(engine.highlighted(), null);

  // The key reappears from another source: the highlight resumes by identity.
  engine.applyBatch(gen, 'c', [candidate('c1', 'Alpha', 0.1)]);
  assert.equal(engine.highlighted()!.key, 'alpha');
  assert.equal(engine.highlighted()!.score, 0.1);
});

test('repeated reads return identical objects for unchanged keys', () => {
  const engine = new SuggestionEngine();
  const gen = engine.beginQuery('q');
  engine.applyBatch(gen, 'a', [candidate('1', 'one', 1), candidate('2', 'two', 2)]);
  const first = engine.suggestions();
  const second = engine.suggestions();
  assert.deepEqual(first, second);
  assert.strictEqual(first[0], second[0]);
  assert.strictEqual(first[1], second[1]);
});

test('moveHighlight walks the sorted list and clamps at the edges', () => {
  const engine = new SuggestionEngine();
  const gen = engine.beginQuery('q');
  engine.applyBatch(gen, 'a', [
    candidate('1', 'low', 0.1),
    candidate('2', 'mid', 0.5),
    candidate('3', 'top', 0.9),
  ]);
  assert.equal(engine.moveHighlight(1)!.key, 'top');
  assert.equal(engine.moveHighlight(1)!.key, 'mid');
  assert.equal(engine.moveHighlight(5)!.key, 'low', 'clamped at the end');
  assert.equal(engine.moveHighlight(-1)!.key, 'mid');
});

test('aggregates out-of-order async sources into the fully sorted result', async () => {
  const engine = new SuggestionEngine();
  const gen = engine.beginQuery('async');

  const batches: Array<readonly [string, readonly Candidate[]]> = [
    ['slow', [candidate('s1', 'shared', 0.2, 'slow'), candidate('s2', 'slow only', 0.9)]],
    ['mid', [candidate('m1', 'SHARED', 0.8, 'mid'), candidate('m2', 'mid only', 0.3)]],
    ['fast', [candidate('f1', ' shared ', 0.5), candidate('f2', 'fast only', 0.95)]],
  ];
  // Resolve in a deliberately scrambled order.
  const delays = [40, 15, 1];
  await Promise.all(
    batches.map(([sourceId, candidates], i) =>
      sleep(delays[i]).then(() => {
        const applied = engine.applyBatch(gen, sourceId, candidates);
        assert.equal(applied, true);
      }),
    ),
  );

  const expected = referenceAggregate(batches);
  const actual = engine.suggestions();
  assert.deepEqual(
    actual.map((s) => ({ key: s.key, score: s.score, sources: [...s.sources].sort() })),
    expected,
    'result equals the full aggregation regardless of arrival order',
  );
  // Arrival order must not leak into the ranking.
  assert.deepEqual(
    actual.map((s) => s.key),
    ['fast only', 'slow only', 'shared', 'mid only'],
  );
});

test('rejects stale generations when the query changes mid-flight', async () => {
  const engine = new SuggestionEngine();

  const gen1 = engine.beginQuery('a');
  engine.applyBatch(gen1, 'instant', [candidate('i1', 'alpha', 0.4)]);

  // The user keeps typing before the slow source for generation 1 resolves.
  const gen2 = engine.beginQuery('al');
  assert.equal(gen2, gen1 + 1);
  engine.applyBatch(gen2, 'fast', [candidate('f1', 'alpine', 0.7)]);

  // The stale batch arrives late and must be ignored.
  assert.equal(engine.applyBatch(gen1, 'slow', [candidate('s1', 'albatross', 0.99)]), false);
  assert.deepEqual(
    engine.suggestions().map((s) => s.key),
    ['alpine'],
    'stale generation cannot pollute the current query',
  );

  // Race both generations concurrently; only generation 2 may land.
  const gen3 = engine.beginQuery('alp');
  await Promise.all([
    sleep(5).then(() => engine.applyBatch(gen2, 'old-fast', [candidate('o1', 'alpha', 1)])),
    sleep(10).then(() => engine.applyBatch(gen3, 'new-slow', [candidate('n1', 'alps', 0.6)])),
    sleep(1).then(() => engine.applyBatch(gen3, 'new-fast', [candidate('n2', 'alpine', 0.9)])),
  ]);
  assert.deepEqual(
    engine.suggestions().map((s) => [s.key, s.score]),
    [
      ['alpine', 0.9],
      ['alps', 0.6],
    ],
  );
});

test('highlight does not leak across query generations', () => {
  const engine = new SuggestionEngine();
  const gen1 = engine.beginQuery('one');
  engine.applyBatch(gen1, 'a', [candidate('1', 'shared', 0.5)]);
  engine.highlight('shared');
  assert.equal(engine.highlighted()!.key, 'shared');

  const gen2 = engine.beginQuery('two');
  engine.applyBatch(gen2, 'a', [candidate('2', 'shared', 0.5)]);
  assert.equal(engine.highlighted(), null, 'new query resets the highlight');
});

test('empty and blank candidates are ignored', () => {
  const engine = new SuggestionEngine();
  const gen = engine.beginQuery('q');
  engine.applyBatch(gen, 'a', [candidate('1', '   ', 1), candidate('2', '', 1)]);
  assert.deepEqual(engine.suggestions(), []);
});

test('suggestion view type is exposed for consumers', () => {
  const view: Suggestion = {
    key: 'k',
    score: 1,
    sources: new Set(['s']),
    explanations: new Map(),
  };
  assert.equal(view.key, 'k');
});
