import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeController } from '../src/controller.ts';
import type { NodeData } from '../src/types.ts';

const base: NodeData[] = [
  { id: 'alpha', label: 'alpha', children: [{ id: 'a1', label: 'a1' }] },
  { id: 'remote', label: 'remote', hasChildren: true },
  { id: 'omega', label: 'omega' },
];

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (v: T) => void;
  reject: (e: unknown) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

const flush = (): Promise<void> => new Promise((r) => setImmediate(r));

const kids = (prefix: string): NodeData[] => [
  { id: `${prefix}-1`, label: `${prefix} one` },
  { id: `${prefix}-2`, label: `${prefix} two` },
];

test('a stale load must not re-expand a branch the user closed', async () => {
  const c = new TreeController(base);
  const d = deferred<NodeData[]>();
  c.expand('remote');
  const p = c.loadChildren('remote', () => d.promise);
  c.collapse('remote'); // user closes the branch before the load returns
  d.resolve(kids('r'));
  await p;
  assert.ok(!c.tree.isExpanded('remote'), 'branch stays collapsed');
  assert.deepEqual([...c.tree.childrenOf('remote')], ['r-1', 'r-2'], 'children still inserted');
  assert.deepEqual([...c.visible.ids], ['alpha', 'remote', 'omega'], 'children hidden');
  c.expand('remote');
  assert.deepEqual([...c.visible.ids], ['alpha', 'remote', 'r-1', 'r-2', 'omega']);
});

test('only the latest load may complete; earlier results are discarded', async () => {
  const c = new TreeController(base);
  const d1 = deferred<NodeData[]>();
  const d2 = deferred<NodeData[]>();
  const p1 = c.loadChildren('remote', () => d1.promise);
  const p2 = c.loadChildren('remote', () => d2.promise); // supersedes p1
  d1.resolve(kids('stale'));
  d2.resolve(kids('fresh'));
  const [r1, r2] = await Promise.all([p1, p2]);
  assert.equal(r1, null, 'stale load discarded');
  assert.deepEqual(r2, ['fresh-1', 'fresh-2']);
  assert.deepEqual([...c.tree.childrenOf('remote')], ['fresh-1', 'fresh-2']);
});

test('cancelling a load discards its late result', async () => {
  const c = new TreeController(base);
  const d = deferred<NodeData[]>();
  const p = c.loadChildren('remote', () => d.promise);
  c.cancelLoad('remote');
  d.resolve(kids('r'));
  const r = await p;
  assert.equal(r, null);
  assert.deepEqual([...c.tree.childrenOf('remote')], []);
  assert.ok(c.tree.hasLazyChildren('remote'), 'still loadable later');
});

test('a load resolving after its node was removed is discarded', async () => {
  const c = new TreeController(base);
  const d = deferred<NodeData[]>();
  const p = c.loadChildren('remote', () => d.promise);
  c.removeNode('remote');
  d.resolve(kids('r'));
  const r = await p;
  assert.equal(r, null);
  assert.ok(!c.tree.exists('r-1'));
});

test('failed loads leave the branch loadable and do not corrupt state', async () => {
  const c = new TreeController(base);
  const d = deferred<NodeData[]>();
  const p = c.loadChildren('remote', () => d.promise);
  d.reject(new Error('network down'));
  await assert.rejects(p);
  assert.ok(c.tree.hasLazyChildren('remote'));
  assert.ok(!c.tree.isLoadPending('remote'));
});

test('async children never reset focus or selection', async () => {
  const c = new TreeController(base);
  c.expand('alpha');
  c.expand('remote');
  c.click('a1');
  c.toggleSelect('omega');
  const d = deferred<NodeData[]>();
  const p = c.loadChildren('remote', () => d.promise);
  d.resolve(kids('r'));
  await p;
  assert.equal(c.focus.id, 'a1');
  assert.deepEqual([...c.selection.selected].sort(), ['a1', 'omega']);
  assert.deepEqual([...c.visible.ids], ['alpha', 'a1', 'remote', 'r-1', 'r-2', 'omega']);
});

test('expanding a lazy branch auto-starts the configured loader', async () => {
  const c = new TreeController(base, { loader: async () => kids('auto') });
  c.expand('remote');
  assert.ok(c.tree.isLoadPending('remote'));
  await flush();
  assert.deepEqual([...c.tree.childrenOf('remote')], ['auto-1', 'auto-2']);
});
