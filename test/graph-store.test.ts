import assert from 'node:assert/strict';
import test from 'node:test';
import { PendingRefError, PublishError } from '../src/errors.ts';
import type { Key, NodeId } from '../src/model.ts';
import { GraphStore } from '../src/store.ts';
import type { View } from '../src/view.ts';

type Point = [string, Key[]];

// Identity-relation matrix: entry [i][j] is true iff the nodes at points
// i and j are the *same identity* (not merely equal content).
function identityMatrix(view: View, points: Point[]): boolean[][] {
  const ids: NodeId[] = points.map(([doc, path]) => {
    const probe = view.at(doc, path);
    assert.equal(probe.type, 'node', `expected node at ${doc}:${path.join('/')}`);
    return probe.nodeId;
  });
  return ids.map((a) => ids.map((b) => a === b));
}

function nodeIdAt(view: View, doc: string, path: Key[] = []): NodeId {
  const probe = view.at(doc, path);
  assert.equal(probe.type, 'node');
  return probe.nodeId;
}

function catchPublish(fn: () => unknown): PublishError {
  try {
    fn();
  } catch (err) {
    assert.ok(err instanceof PublishError, `expected PublishError, got ${err}`);
    return err;
  }
  assert.fail('expected publish to throw');
}

test('two roots share an object; updating one branch copy leaves the other root untouched', () => {
  const store = new GraphStore();
  store.load({
    docA: { shared: { $id: 's', name: 'original', tags: ['a', 'b'] } },
    docB: { mirror: { $ref: 's' } },
  });
  const v1 = store.current;

  // Before the update both roots resolve to the same identity.
  assert.equal(nodeIdAt(store.view(), 'docA', ['shared']), nodeIdAt(store.view(), 'docB', ['mirror']));
  const sharedV1 = nodeIdAt(store.view(), 'docA', ['shared']);
  const backRefsV1 = store.backRefs(sharedV1, v1);
  assert.deepEqual(
    backRefsV1.map((r) => r.via).sort(),
    ['child', 'ref'],
    'shared node has one containment parent and one referrer in v1',
  );

  // Copy-on-write through docB's ref edge materializes a private copy.
  const draft = store.beginDraft();
  draft.set('docB', ['mirror', 'name'], 'changed');
  const v2 = draft.publish();
  assert.equal(v2, 2);

  const a2 = nodeIdAt(store.view(), 'docA', ['shared']);
  const b2 = nodeIdAt(store.view(), 'docB', ['mirror']);
  assert.notEqual(a2, b2, 'identities diverge after COW');
  assert.equal(a2, sharedV1, 'docA keeps the original node');
  assert.equal(store.view().value('docA', ['shared', 'name']), 'original');
  assert.equal(store.view().value('docB', ['mirror', 'name']), 'changed');
  // Same content does not mean same identity: copy content differs only in `name`.
  assert.deepEqual(store.view().value('docB', ['mirror', 'tags']), ['a', 'b']);

  // Back-references reflect the unsharing in v2.
  assert.deepEqual(store.backRefs(a2, v2).map((r) => r.via), ['child']);

  // Old-version readers still see the shared identity and old content.
  assert.equal(nodeIdAt(store.view(v1), 'docA', ['shared']), sharedV1);
  assert.equal(nodeIdAt(store.view(v1), 'docB', ['mirror']), sharedV1);
  assert.equal(store.view(v1).value('docB', ['mirror', 'name']), 'original');
});

test('self-loop survives export and re-import with the same identity', () => {
  const store = new GraphStore();
  store.load({ docC: { $id: 'c', label: 'loop', self: { $ref: 'c' } } });

  const selfId = nodeIdAt(store.view(), 'docC', []);
  assert.equal(nodeIdAt(store.view(), 'docC', ['self']), selfId, 'self edge resolves to itself');

  const exported = store.export(['docC']);
  assert.deepEqual(exported.docs['docC'], { $id: 'c', label: 'loop', self: { $ref: 'c' } });

  const restored = new GraphStore();
  restored.load(exported.docs);
  const restoredRoot = nodeIdAt(restored.view(), 'docC', []);
  assert.equal(nodeIdAt(restored.view(), 'docC', ['self']), restoredRoot, 'cycle restored as identity');
  assert.equal(restored.view().value('docC', ['label']), 'loop');
});

test('deleting a document leaves references pending, not null', () => {
  const store = new GraphStore();
  store.load({
    docA: { link: { $ref: 'b-root' }, note: 'keep me' },
    docB: { $id: 'b-root', v: 1 },
  });
  const v1 = store.current;

  const draft = store.beginDraft();
  draft.deleteDocument('docB');

  const slot = draft.slotInfo('docA', ['link']);
  assert.deepEqual(slot, { type: 'ref', refId: 'b-root', status: 'pending' });
  const probe = draft.at('docA', ['link']);
  assert.deepEqual(probe, { type: 'pending-ref', refId: 'b-root' });
  assert.notEqual(probe.type, 'prim', 'pending ref is never silently nulled');
  assert.throws(() => draft.value('docA', ['link', 'v']), PendingRefError);
  assert.deepEqual(draft.pendingRefs(), [{ path: '/docs/docA/link', refId: 'b-root' }]);

  // Publishing with a dangling ref is rejected; the error carries the path.
  const err = catchPublish(() => draft.publish());
  assert.equal(err.issues.length, 1);
  assert.equal(err.issues[0].code, 'dangling-ref');
  assert.equal(err.issues[0].path, '/docs/docA/link');

  // Store is untouched; old-version readers resolve the ref as before.
  assert.equal(store.current, v1);
  assert.equal(store.view().value('docA', ['link', 'v']), 1);
});

test('a failing update rolls back entirely; a fixed draft then publishes', () => {
  const store = new GraphStore();
  store.load({ docA: { x: 1 }, docB: { y: 2 } });
  const rootA1 = nodeIdAt(store.view(), 'docA', []);
  const rootB1 = nodeIdAt(store.view(), 'docB', []);

  const draft = store.beginDraft();
  draft.set('docA', ['x'], 10); // valid local update
  draft.set('docB', ['bad'], { $ref: 'ghost' }); // introduces a dangling ref
  const err = catchPublish(() => draft.publish());
  assert.equal(err.issues[0].code, 'dangling-ref');
  assert.equal(err.issues[0].path, '/docs/docB/bad');

  // Full rollback: version, identities and content are exactly as before.
  assert.equal(store.current, 1);
  assert.equal(nodeIdAt(store.view(), 'docA', []), rootA1);
  assert.equal(nodeIdAt(store.view(), 'docB', []), rootB1);
  assert.equal(store.view().value('docA', ['x']), 1);
  assert.deepEqual(store.view().materialize('docB'), { y: 2 });

  const fixed = store.beginDraft();
  fixed.set('docA', ['x'], 10);
  assert.equal(fixed.publish(), 2);
  assert.equal(store.view().value('docA', ['x']), 10);
});

test('gc never reclaims nodes pinned by old snapshots', () => {
  const store = new GraphStore();
  store.load({
    keep: { k: 1 },
    old: { $id: 'old-root', data: [1, 2, 3] },
  });
  const oldRoot = nodeIdAt(store.view(), 'old', []);
  const keepRoot = nodeIdAt(store.view(), 'keep', []);

  const draft = store.beginDraft();
  draft.deleteDocument('old');
  assert.equal(draft.publish(), 2);

  // v1 is still retained: the removed document's nodes are pinned.
  const firstSweep = store.gc();
  assert.ok(!firstSweep.includes(oldRoot), 'old snapshot pins its nodes');
  assert.equal(nodeIdAt(store.view(1), 'old', []), oldRoot, 'v1 readers still work');

  store.releaseVersion(1);
  const secondSweep = store.gc();
  assert.ok(secondSweep.includes(oldRoot), 'collectable once unpinned');
  assert.ok(!secondSweep.includes(keepRoot), 'live roots are never collected');
  assert.equal(nodeIdAt(store.view(), 'keep', []), keepRoot);
});

test('round-trip preserves the identity-relation matrix and content, not just JSON text', () => {
  const store = new GraphStore();
  store.load({
    main: {
      alpha: { $id: 'shared', n: 1 },
      beta: { $ref: 'shared' },
      twin1: { n: 1 },
    },
    other: {
      gamma: { $ref: 'shared' },
      twin2: { n: 1 },
      local: { secret: 'pruned-away' },
    },
  });

  const points: Point[] = [
    ['main', ['alpha']],
    ['main', ['beta']],
    ['other', ['gamma']],
    ['main', ['twin1']],
    ['other', ['twin2']],
  ];
  const before = identityMatrix(store.view(), points);
  // alpha, beta, gamma are one identity; the twins are content-equal but distinct.
  assert.deepEqual(before, [
    [true, true, true, false, false],
    [true, true, true, false, false],
    [true, true, true, false, false],
    [false, false, false, true, false],
    [false, false, false, false, true],
  ]);

  const exported = store.export(['main', 'other']);
  const restored = new GraphStore();
  restored.load(exported.docs);

  const after = identityMatrix(restored.view(), points);
  assert.deepEqual(after, before, 'identity relations survive the round-trip');
  assert.deepEqual(restored.view().materialize('main'), store.view().materialize('main'));
  assert.deepEqual(restored.view().materialize('other'), store.view().materialize('other'));
  assert.deepEqual(restored.view().value('main', ['twin1']), restored.view().value('other', ['twin2']));

  // Root-pruned export: unreachable content of non-exported roots is excluded.
  const pruned = store.export(['main']);
  assert.deepEqual(Object.keys(pruned.docs), ['main']);
  assert.ok(!JSON.stringify(pruned.docs).includes('pruned-away'));
  // ...while the shared node referenced from the pruned doc is still intact.
  assert.equal(nodeIdAt(store.view(), 'other', ['gamma']), nodeIdAt(store.view(), 'main', ['alpha']));
});

test('type-mismatched ref blocks publication; old version stays readable', () => {
  const store = new GraphStore();
  store.load({
    lib: { $id: 'cfg', mode: 'x' },
    app: { name: 'a' },
  });

  const draft = store.beginDraft();
  draft.set('app', ['item'], { $ref: 'cfg', $expect: 'array' });
  const slot = draft.slotInfo('app', ['item']);
  assert.equal(slot.type, 'ref');
  assert.equal(slot.status, 'type-mismatch');

  const err = catchPublish(() => draft.publish());
  assert.equal(err.issues[0].code, 'type-mismatch');
  assert.equal(err.issues[0].path, '/docs/app/item');
  assert.equal(store.current, 1);
  assert.equal(store.view().value('lib', ['mode']), 'x');

  const fixed = store.beginDraft();
  fixed.set('app', ['item'], { $ref: 'cfg', $expect: 'object' });
  assert.equal(fixed.publish(), 2);
  assert.equal(nodeIdAt(store.view(), 'app', ['item']), nodeIdAt(store.view(), 'lib', []));
});

test('object key order is insignificant, array order is significant', () => {
  const a = new GraphStore();
  a.load({ d: { obj: { a: 1, b: 2, c: 3 }, arr: [{ x: 1 }, { y: 2 }] } });
  const b = new GraphStore();
  b.load({ d: { obj: { c: 3, b: 2, a: 1 }, arr: [{ x: 1 }, { y: 2 }] } });

  // Canonical export sorts object keys: both loads export identically.
  assert.equal(JSON.stringify(a.export(['d'])), JSON.stringify(b.export(['d'])));
  assert.deepEqual(a.view().materialize('d'), b.view().materialize('d'));

  // Array order is preserved through export.
  assert.deepEqual(a.view().value('d', ['arr']), [{ x: 1 }, { y: 2 }]);

  const swapped = new GraphStore();
  swapped.load({ d: { arr: [{ y: 2 }, { x: 1 }] } });
  assert.notDeepEqual(swapped.view().value('d', ['arr']), a.view().value('d', ['arr']));
});
