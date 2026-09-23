import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store, type Notification } from '../src/store.ts';
import { Automaton } from '../src/automaton.ts';
import { matchPatternRef, matchPatternRefCounted } from '../src/reference.ts';
import { formatPath } from '../src/path.ts';

function lcg(seed: number): () => number {
  let s = seed >>> 0;
  return () => ((s = (s * 1664525 + 1013904223) >>> 0)) / 2 ** 32;
}

// ---------------------------------------------------------------------------
// Subscription semantics
// ---------------------------------------------------------------------------

test('parent path subscription receives child changes (subtree semantics)', () => {
  const store = new Store({ a: { b: { c: 1 } } });
  const notes: Notification[] = [];
  store.subscribe('/a/b', n => notes.push(n));
  store.tx(t => t.set('/a/b/c/d/e', 42));
  assert.equal(notes.length, 1);
  assert.equal(notes[0].ops.length, 1);
  assert.equal(notes[0].ops[0].kind, 'set');
  assert.equal(store.get('/a/b/c/d/e'), 42);
});

test('adjacent string prefixes are NOT ancestors', () => {
  const store = new Store({ ab: 1, abc: 2 });
  let hits = 0;
  store.subscribe('/ab', () => hits++);
  store.tx(t => t.set('/abc', 20)); // 'ab' is a string prefix of 'abc' but not a segment ancestor
  assert.equal(hits, 0);
  store.tx(t => t.set('/ab', 10));
  assert.equal(hits, 1);
  store.tx(t => t.set('/ab/c/d', 5)); // genuine child of /ab
  assert.equal(hits, 2);
});

test('wildcards: * matches exactly one segment, ** matches any depth', () => {
  const store = new Store({});
  const hits = { star: 0, deep: 0 };
  store.subscribe('/u/*/name', () => hits.star++);
  store.subscribe('/u/**', () => hits.deep++);
  store.tx(t => t.set('/u/alice/name', 'A'));
  assert.deepEqual(hits, { star: 1, deep: 1 });
  store.tx(t => t.set('/u/alice/profile/name', 'B')); // too deep for '*'
  assert.deepEqual(hits, { star: 1, deep: 2 });
  store.tx(t => t.set('/u', { reset: true })); // '/u' itself still under '/u/**'
  assert.deepEqual(hits, { star: 1, deep: 3 });
});

test('root pattern / matches every change', () => {
  const store = new Store({});
  let hits = 0;
  store.subscribe('/', () => hits++);
  store.tx(t => {
    t.set('/x', 1);
    t.set('/a/b/c', 2);
  });
  assert.equal(hits, 1); // one notification, both ops inside
});

// ---------------------------------------------------------------------------
// Moves: old+new path diffs, de-duplication, op order
// ---------------------------------------------------------------------------

test('move notifies subscribers on both old and new paths', () => {
  const store = new Store({ a: { x: { v: 1 } } });
  const seen: string[] = [];
  store.subscribe('/a/x', () => seen.push('old-side'));
  store.subscribe('/b/y', () => seen.push('new-side'));
  store.subscribe('/a/x/v', () => seen.push('old-leaf'));
  store.subscribe('/b/y/v', () => seen.push('new-leaf'));
  store.tx(t => t.move('/a/x', '/b/y'));
  assert.deepEqual(seen.sort(), ['new-leaf', 'new-side', 'old-leaf', 'old-side']);
  assert.equal(store.get('/a/x'), undefined);
  assert.deepEqual(store.get('/b/y'), { v: 1 });
});

test('duplicate matches collapse to one notification; op identity and order preserved', () => {
  const store = new Store({ a: { x: { v: 1, w: { u: 2 } } } });
  const notes: Notification[] = [];
  store.subscribe('/a', n => notes.push(n)); // matches BOTH ends of the move
  store.tx(t => {
    t.set('/a/p', 1);
    t.move('/a/x', '/a/y'); // old /a/x... and new /a/y... both match '/a'
    t.set('/a/q', 2);
  });
  assert.equal(notes.length, 1, 'one notification per subscription per commit');
  assert.deepEqual(notes[0].ops.map(o => o.kind), ['set', 'move', 'set'], 'commit order preserved');
  assert.equal(new Set(notes[0].ops.map(o => o.id)).size, 3, 'each op appears exactly once');
  const moveChanges = notes[0].changes.filter(c => c.kind === 'move');
  assert.equal(moveChanges.length, 4, 'moved subtree of 4 nodes produced 4 old/new diffs');
  assert.ok(moveChanges.every(c => c.oldPath && c.path));
  assert.deepEqual(moveChanges[0].oldPath, ['a', 'x']);
  assert.deepEqual(moveChanges[0].path, ['a', 'y']);
});

test('move validates source and destination', () => {
  const store = new Store({ a: { b: 1 } });
  assert.throws(() => store.tx(t => t.move('/nope', '/x')), /nothing at/);
  assert.throws(() => store.tx(t => t.move('/a', '/a/b/c')), /own subtree/);
  assert.deepEqual(store.snapshot(), { a: { b: 1 } }, 'failed tx leaves state untouched');
});

// ---------------------------------------------------------------------------
// Array element deletion: explicit index-shift notification scope
// ---------------------------------------------------------------------------

test('array element delete notifies exactly the shifted index range i..n-1', () => {
  const store = new Store({ arr: ['a', 'b', 'c', 'd', 'e'] });
  const hits = new Map<string, number>();
  const bump = (p: string) => hits.set(p, (hits.get(p) ?? 0) + 1);
  for (const p of ['/arr', '/arr/0', '/arr/1', '/arr/2', '/arr/3', '/arr/4', '/arr/5']) {
    store.subscribe(p, () => bump(p));
  }
  store.tx(t => t.delete('/arr/1'));
  assert.deepEqual(store.get('/arr'), ['a', 'c', 'd', 'e']);
  assert.equal(hits.get('/arr'), 1, 'parent array path notified');
  assert.equal(hits.get('/arr/0'), undefined, 'index 0 is before the shift range: untouched');
  for (const p of ['/arr/1', '/arr/2', '/arr/3', '/arr/4']) {
    assert.equal(hits.get(p), 1, `${p} is inside the explicit shift scope 1..4`);
  }
  assert.equal(hits.get('/arr/5'), undefined, 'index 5 never existed: outside scope');
});

test('shift changes carry old and new index paths', () => {
  const store = new Store({ arr: ['a', 'b', 'c'] });
  const notes: Notification[] = [];
  store.subscribe('/arr/1', n => notes.push(n));
  store.tx(t => t.delete('/arr/0'));
  assert.equal(notes.length, 1);
  const shifts = notes[0].changes.filter(c => c.kind === 'shift');
  assert.deepEqual(
    shifts.map(c => [formatPath(c.oldPath!), formatPath(c.path)]),
    [['/arr/1', '/arr/0'], ['/arr/2', '/arr/1']],
  );
});

// ---------------------------------------------------------------------------
// Transaction boundaries: isolation, staged subscriptions, reentrancy
// ---------------------------------------------------------------------------

test('readers never observe intermediate transaction state', () => {
  const store = new Store({ a: 1, b: 1 });
  const during: unknown[] = [];
  store.tx(t => {
    t.set('/a', 2);
    during.push(store.get('/a')); // external reader: still committed state
    t.set('/b', 2);
    during.push(store.get('/b'));
    during.push(t.get('/a')); // tx reads its own writes
  });
  assert.deepEqual(during, [1, 1, 2]);
  assert.equal(store.get('/a'), 2);
  assert.equal(store.get('/b'), 2);
});

test('snapshots are immutable across commits (copy-on-write)', () => {
  const store = new Store({ a: { b: 1 } });
  const before = store.snapshot();
  store.tx(t => t.set('/a/b', 99));
  assert.deepEqual(before, { a: { b: 1 } }, 'previous snapshot unchanged');
});

test('subscribe/unsubscribe inside a transaction take effect at its boundary', () => {
  const store = new Store({});
  const got: string[] = [];
  store.tx(t => {
    store.subscribe('/a', () => got.push('sub'));
    t.set('/a', 1);
  });
  assert.deepEqual(got, [], 'staged subscription does not observe its own transaction');
  store.tx(t => t.set('/a', 2));
  assert.deepEqual(got, ['sub'], 'active from the next transaction on');

  const h = store.subscribe('/b', () => got.push('b'));
  store.tx(t => {
    h.unsubscribe();
    t.set('/b', 1);
  });
  assert.deepEqual(got, ['sub', 'b'], 'staged cancellation still receives the current commit');
  store.tx(t => t.set('/b', 2));
  assert.deepEqual(got, ['sub', 'b'], 'inactive after the boundary');
});

test('unsubscribe outside a transaction takes effect immediately', () => {
  const store = new Store({});
  let hits = 0;
  const h = store.subscribe('/a', () => hits++);
  h.unsubscribe();
  store.tx(t => t.set('/a', 1));
  assert.equal(hits, 0);
});

test('reentrant writes commit after the current delivery round', () => {
  const store = new Store({ x: 0, y: 0 });
  const events: string[] = [];
  store.subscribe('/x', () => {
    events.push(`x sees y=${store.get('/y')}`);
    if (store.get('/y') === 0) store.tx(t => t.set('/y', 1)); // reentrant write
    events.push(`x still sees y=${store.get('/y')}`);
  });
  store.subscribe('/y', () => events.push(`y=${store.get('/y')}`));
  store.tx(t => t.set('/x', 1));
  assert.deepEqual(events, [
    'x sees y=0',
    'x still sees y=0', // reentrant tx not yet committed mid-delivery
    'y=1',              // committed right after, in its own round
  ]);
});

// ---------------------------------------------------------------------------
// Shared automaton vs. independent reference matcher
// ---------------------------------------------------------------------------

test('differential: shared automaton agrees with per-subscription reference matching', () => {
  const rand = lcg(42);
  const names = ['a', 'b', 'c', 'd'];
  const randSeg = () => names[Math.floor(rand() * names.length)];
  const randPatSeg = () => {
    const r = rand();
    return r < 0.15 ? '*' : r < 0.25 ? '**' : randSeg();
  };
  const auto = new Automaton();
  const pats: string[][] = [];
  for (let i = 0; i < 40; i++) {
    const p = Array.from({ length: Math.floor(rand() * 5) }, randPatSeg);
    pats.push(p);
    auto.add(i, p);
  }
  for (let iter = 0; iter < 2000; iter++) {
    const path = Array.from({ length: Math.floor(rand() * 8) }, randSeg);
    const got = auto.match(path).subs;
    const want = pats.map((_, i) => i).filter(i => matchPatternRef(pats[i], path));
    assert.deepEqual(got, want, `path=${JSON.stringify(path)}`);
  }
});

test('long paths + shared prefixes: automaton traverses far fewer states (metric recorded)', () => {
  const shared = Array.from({ length: 100 }, (_, i) => `seg${i}`);
  const auto = new Automaton();
  const patterns: string[][] = [];
  for (let k = 0; k < 50; k++) {
    const p = [...shared, `leaf${k}`];
    patterns.push(p);
    auto.add(k, p);
  }
  const path = [...shared, 'leaf7', 'extra', 'deep'];
  const r = auto.match(path);
  let refVisited = 0;
  const refSubs: number[] = [];
  patterns.forEach((p, i) => {
    const rr = matchPatternRefCounted(p, path);
    refVisited += rr.visited;
    if (rr.match) refSubs.push(i);
  });
  assert.deepEqual(r.subs, refSubs, 'same matches');
  console.log(
    `    [metric] path depth=${path.length}, patterns=${patterns.length}, ` +
    `automaton states=${auto.stateCount}, automaton visited=${r.visited}, ` +
    `reference visited=${refVisited}, ratio=${(refVisited / r.visited).toFixed(1)}x`,
  );
  assert.ok(r.visited * 10 < refVisited, 'shared automaton is at least 10x cheaper here');
});

// ---------------------------------------------------------------------------
// Store-level verification + replay
// ---------------------------------------------------------------------------

test('random op sequences: reference verification passes and op log replays', () => {
  const rand = lcg(7);
  const store = new Store({});
  const sinks = new Map<number, Notification[]>();
  const patSegs = ['r', 'a', 'b', '*', '**'];
  for (let i = 0; i < 12; i++) {
    const pat = '/' + Array.from({ length: 1 + Math.floor(rand() * 3) }, () =>
      patSegs[Math.floor(rand() * patSegs.length)]).join('/');
    const h = store.subscribe(pat, n => sinks.get(h.id)!.push(n));
    sinks.set(h.id, []);
  }

  const existing = new Set<string>(); // paths the model believes exist
  let moveCounter = 0;
  const pickExisting = (): string | null => {
    const arr = [...existing].filter(p => !p.startsWith('/moved'));
    return arr.length ? arr[Math.floor(rand() * arr.length)] : null;
  };
  const addWithAncestors = (p: string) => {
    const segs = p.split('/').filter(Boolean);
    for (let i = 1; i <= segs.length; i++) existing.add('/' + segs.slice(0, i).join('/'));
  };
  const removeSubtree = (p: string) => {
    for (const e of [...existing]) if (e === p || e.startsWith(p + '/')) existing.delete(e);
  };

  for (let round = 0; round < 60; round++) {
    store.tx(t => {
      const opCount = 1 + Math.floor(rand() * 4);
      for (let k = 0; k < opCount; k++) {
        const choice = rand();
        if (choice < 0.5 || existing.size === 0) {
          const p = `/r/${Math.floor(rand() * 4)}/${Math.floor(rand() * 4)}`;
          t.set(p, Math.floor(rand() * 100));
          addWithAncestors(p);
        } else if (choice < 0.75) {
          const p = pickExisting();
          if (p) {
            t.delete(p);
            removeSubtree(p);
          }
        } else {
          const p = pickExisting();
          if (p) {
            const to = `/moved/${moveCounter++}`;
            t.move(p, to);
            const moved = [...existing].filter(e => e === p || e.startsWith(p + '/'));
            removeSubtree(p);
            for (const m of moved) existing.add(to + m.slice(p.length));
          }
        }
      }
    });
    assert.deepEqual(store.verifyLastCommit(), [], `reference verification failed at round ${round}`);
    assert.ok(store.stats.lastMatchVisited > 0, 'traversal metric recorded');
  }

  const replica = Store.replay(store.opLog);
  assert.deepEqual(replica.snapshot(), store.snapshot(), 'replayed op log reproduces state');
});

test('deleting a subtree notifies subscribers of its descendants', () => {
  const store = new Store({ a: { b: { c: 1, d: 2 } } });
  const hits: string[] = [];
  store.subscribe('/a/b/c', () => hits.push('c'));
  store.subscribe('/a/b', () => hits.push('b'));
  store.tx(t => t.delete('/a'));
  assert.deepEqual(hits.sort(), ['b', 'c']);
  assert.equal(store.get('/a'), undefined);
});
