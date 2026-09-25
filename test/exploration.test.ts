import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeController } from '../src/controller.ts';
import { fixtureTree } from '../fixtures/tree.fixture.ts';
import type { NodeData, NodeId } from '../src/types.ts';

/**
 * Bounded event exploration: seeded random walks over the controller's event
 * alphabet (navigation, selection, type-ahead, expand/collapse, async load
 * begin/resolve/cancel, insert/remove/move, filter, disable). After every
 * event we assert the core invariants; afterwards the recorded event log is
 * replayed from scratch and must reproduce the exact same state.
 */

type Op =
  | { kind: 'noop' }
  | { kind: 'expand' | 'collapse' | 'toggleExpanded'; id: NodeId }
  | { kind: 'key'; key: string; shift: boolean }
  | { kind: 'click'; id: NodeId; ctrl: boolean; shift: boolean }
  | { kind: 'typeChar'; ch: string }
  | { kind: 'insert'; parentId: NodeId | null; node: NodeData; index: number }
  | { kind: 'remove'; id: NodeId }
  | { kind: 'move'; id: NodeId; parentId: NodeId | null; index: number }
  | { kind: 'filter'; text: string }
  | { kind: 'setDisabled'; id: NodeId; disabled: boolean }
  | { kind: 'setLabel'; id: NodeId; label: string }
  | { kind: 'beginLoad'; id: NodeId }
  | { kind: 'resolveLoad'; children: NodeData[] }
  | { kind: 'cancelLoad'; id: NodeId };

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface Ctx {
  pending: { id: NodeId; resolve: (v: NodeData[]) => void }[];
  promises: Promise<unknown>[];
  counter: number;
}

const flush = (): Promise<void> => new Promise((r) => setImmediate(r));

function pick<T>(rng: () => number, arr: readonly T[]): T {
  return arr[Math.floor(rng() * arr.length)];
}

function genOp(ctrl: TreeController, ctx: Ctx, rng: () => number): Op {
  const ids = ctrl.tree.allIds();
  const kind = pick(rng, [
    'expand', 'expand', 'collapse', 'collapse', 'toggleExpanded',
    'key', 'key', 'key', 'click', 'click', 'typeChar', 'typeChar',
    'insert', 'remove', 'move', 'filter', 'setDisabled', 'setLabel',
    'beginLoad', 'resolveLoad', 'cancelLoad',
  ] as const);
  switch (kind) {
    case 'expand':
    case 'collapse':
    case 'toggleExpanded':
      return { kind, id: pick(rng, ids) };
    case 'key':
      return {
        kind,
        key: pick(rng, ['ArrowDown', 'ArrowUp', 'Home', 'End', 'ArrowLeft', 'ArrowRight', ' ', 'Escape'] as const),
        shift: rng() < 0.3,
      };
    case 'click': {
      const vis = ctrl.visible.ids;
      if (vis.length === 0) return { kind: 'noop' };
      return { kind, id: pick(rng, [...vis]), ctrl: rng() < 0.4, shift: rng() < 0.4 };
    }
    case 'typeChar':
      return { kind, ch: pick(rng, ['a', 'b', 'g', 'd', 'r', 'o', 'x', '1'] as const) };
    case 'insert': {
      const n = ++ctx.counter;
      return {
        kind,
        parentId: rng() < 0.3 ? null : pick(rng, ids),
        node: { id: `x${n}`, label: `xnode ${n}`, disabled: n % 7 === 0 },
        index: rng() < 0.5 ? 0 : 1,
      };
    }
    case 'remove':
      if (ids.length <= 1) return { kind: 'noop' };
      return { kind, id: pick(rng, ids) };
    case 'move':
      return {
        kind,
        id: pick(rng, ids),
        parentId: rng() < 0.3 ? null : pick(rng, ids),
        index: Math.floor(rng() * 3),
      };
    case 'filter':
      return { kind, text: pick(rng, ['', 'one', 'a', 'zz', 'xnode', 'load'] as const) };
    case 'setDisabled':
      return { kind, id: pick(rng, ids), disabled: rng() < 0.5 };
    case 'setLabel': {
      const n = ++ctx.counter;
      return { kind, id: pick(rng, ids), label: `renamed ${n}` };
    }
    case 'beginLoad': {
      const lazy = ids.filter((id) => ctrl.tree.hasLazyChildren(id));
      if (lazy.length === 0) return { kind: 'noop' };
      return { kind, id: pick(rng, lazy) };
    }
    case 'resolveLoad': {
      if (ctx.pending.length === 0) return { kind: 'noop' };
      const n = ++ctx.counter;
      return {
        kind,
        children: [
          { id: `l${n}a`, label: `load ${n} a` },
          { id: `l${n}b`, label: `load ${n} b`, disabled: n % 5 === 0 },
        ],
      };
    }
    case 'cancelLoad': {
      const pendingIds = [...new Set(ctx.pending.map((p) => p.id))];
      if (pendingIds.length === 0) return { kind: 'noop' };
      return { kind, id: pick(rng, pendingIds) };
    }
  }
}

async function applyOp(ctrl: TreeController, op: Op, ctx: Ctx): Promise<void> {
  switch (op.kind) {
    case 'noop':
      return;
    case 'expand':
      ctrl.expand(op.id);
      return;
    case 'collapse':
      ctrl.collapse(op.id);
      return;
    case 'toggleExpanded':
      ctrl.toggleExpanded(op.id);
      return;
    case 'key':
      ctrl.handleKey(op.key, { shift: op.shift });
      return;
    case 'click':
      ctrl.click(op.id, { ctrl: op.ctrl, shift: op.shift });
      return;
    case 'typeChar':
      ctrl.typeChar(op.ch);
      return;
    case 'insert':
      if (op.parentId === null || ctrl.tree.exists(op.parentId)) {
        ctrl.insertChildren(op.parentId, [structuredClone(op.node)], op.index);
      }
      return;
    case 'remove':
      ctrl.removeNode(op.id);
      return;
    case 'move':
      ctrl.moveNode(op.id, op.parentId, op.index);
      return;
    case 'filter':
      ctrl.setFilter(op.text);
      return;
    case 'setDisabled':
      ctrl.setDisabled(op.id, op.disabled);
      return;
    case 'setLabel':
      ctrl.tree.setLabel(op.id, op.label);
      return;
    case 'beginLoad': {
      const p = ctrl.loadChildren(
        op.id,
        () => new Promise<NodeData[]>((resolve) => ctx.pending.push({ id: op.id, resolve })),
      );
      p.catch(() => {});
      ctx.promises.push(p);
      return;
    }
    case 'resolveLoad': {
      const p = ctx.pending.shift();
      if (p) {
        p.resolve(structuredClone(op.children));
        await flush();
      }
      return;
    }
    case 'cancelLoad':
      ctrl.cancelLoad(op.id);
      return;
  }
}

function checkInvariants(ctrl: TreeController, step: string): void {
  // 1. The incremental visible sequence equals a full reference traversal.
  assert.deepEqual([...ctrl.visible.ids], ctrl.visible.computeAll(), `${step}: visible sequence`);
  // 2. Focus: null or an existing, enabled, visible row.
  const f = ctrl.focus.id;
  if (f !== null) {
    assert.ok(ctrl.tree.exists(f), `${step}: focus exists`);
    assert.ok(!ctrl.tree.isDisabled(f), `${step}: focus not disabled`);
    assert.ok(ctrl.visible.includes(f), `${step}: focus visible`);
  }
  // 3. Selection: only existing, enabled nodes.
  for (const id of ctrl.selection.selected) {
    assert.ok(ctrl.tree.exists(id), `${step}: selected ${id} exists`);
    assert.ok(!ctrl.tree.isDisabled(id), `${step}: selected ${id} enabled`);
  }
  // 4. Anchor: null or an existing node.
  const a = ctrl.selection.anchor;
  if (a !== null) assert.ok(ctrl.tree.exists(a), `${step}: anchor exists`);
  // 5. Type-ahead buffer is never served stale.
  if (ctrl.typeahead.version !== ctrl.tree.version) {
    assert.equal(ctrl.typeahead.currentBuffer, '', `${step}: stale buffer hidden`);
  }
}

function hashState(ctrl: TreeController): string {
  return JSON.stringify({
    version: ctrl.tree.version,
    visible: [...ctrl.visible.ids],
    focus: ctrl.focus.id,
    focusVersion: ctrl.focus.version,
    selected: [...ctrl.selection.selected].sort(),
    anchor: ctrl.selection.anchor,
    expanded: ctrl.tree.expandedIds().sort(),
    filter: ctrl.tree.filterText,
    order: ctrl.tree.allIds(),
    meta: ctrl.tree.allIds().map((id) => [id, ctrl.tree.labelOf(id), ctrl.tree.isDisabled(id)]),
    buffer: ctrl.typeahead.currentBuffer,
  });
}

async function runExploration(seed: number, steps: number): Promise<{ log: Op[]; hash: string }> {
  const ctrl = new TreeController(structuredClone(fixtureTree));
  const ctx: Ctx = { pending: [], promises: [], counter: 0 };
  const rng = mulberry32(seed);
  const log: Op[] = [];
  for (let i = 0; i < steps; i++) {
    const op = genOp(ctrl, ctx, rng);
    log.push(op);
    await applyOp(ctrl, op, ctx);
    ctrl.sync();
    checkInvariants(ctrl, `seed=${seed} step=${i} op=${JSON.stringify(op)}`);
  }
  for (const p of ctx.pending.splice(0)) p.resolve([]);
  await Promise.allSettled(ctx.promises);
  return { log, hash: hashState(ctrl) };
}

async function replay(log: Op[]): Promise<string> {
  const ctrl = new TreeController(structuredClone(fixtureTree));
  const ctx: Ctx = { pending: [], promises: [], counter: 0 };
  for (let i = 0; i < log.length; i++) {
    await applyOp(ctrl, log[i], ctx);
    ctrl.sync();
    checkInvariants(ctrl, `replay step=${i} op=${JSON.stringify(log[i])}`);
  }
  for (const p of ctx.pending.splice(0)) p.resolve([]);
  await Promise.allSettled(ctx.promises);
  return hashState(ctrl);
}

test('bounded exploration: invariants hold and replay reproduces state', async () => {
  for (const seed of [1, 7, 42, 1337, 20240901]) {
    const { log, hash } = await runExploration(seed, 150);
    const replayed = await replay(log);
    assert.equal(replayed, hash, `replay diverged for seed ${seed}`);
  }
});

test('exploration covers cancel and disabled interactions deterministically', async () => {
  // A hand-written trace exercising cancel + disable + range anchors.
  const trace: Op[] = [
    { kind: 'expand', id: 'alpha' },
    { kind: 'expand', id: 'gamma' },
    { kind: 'expand', id: 'remote' },
    { kind: 'beginLoad', id: 'remote' },
    { kind: 'cancelLoad', id: 'remote' },
    { kind: 'resolveLoad', children: [{ id: 'late', label: 'late child' }] }, // cancelled: discarded
    { kind: 'click', id: 'alpha-one', ctrl: false, shift: false },
    { kind: 'click', id: 'gamma-two', ctrl: false, shift: true },
    { kind: 'setDisabled', id: 'gamma-one', disabled: true }, // inside the range
    { kind: 'key', key: 'ArrowDown', shift: true },
    { kind: 'typeChar', ch: 'r' },
    { kind: 'key', key: 'Escape', shift: false },
    { kind: 'remove', id: 'gamma' },
    { kind: 'filter', text: 'one' },
    { kind: 'filter', text: '' },
  ];
  const hash = await replay(trace);
  const again = await replay(trace);
  assert.equal(again, hash, 'hand-written trace is deterministic');
  const state = JSON.parse(hash);
  assert.ok(!state.order.includes('late'), 'cancelled load left no children');
  assert.ok(state.selected.every((id: string) => id !== 'gamma-one'), 'disabled node not selected');
});
