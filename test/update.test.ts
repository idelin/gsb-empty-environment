import { test } from 'node:test';
import assert from 'node:assert/strict';
import { pack, planUpdate, verifyAll } from '../src/index.ts';
import type { ItemSpec, LayoutSnapshot } from '../src/index.ts';

const cfg = { pageWidth: 32, pageHeight: 32, spacing: 2 };

function snapshotOf(items: ItemSpec[]): LayoutSnapshot {
  const r = pack({ ...cfg, items });
  return { ...cfg, items, placements: r.placements, pages: r.pages };
}

test('removal keeps every other placement exactly in place', () => {
  const items: ItemSpec[] = [
    { id: 'a', width: 8, height: 8 },
    { id: 'b', width: 8, height: 8 },
    { id: 'c', width: 8, height: 8 },
  ];
  const prev = snapshotOf(items);
  const r = planUpdate(prev, { remove: ['b'] });
  assert.ok(r.ok);
  const stays = r.moves.filter((m) => m.kind === 'stay').map((m) => m.id).sort();
  assert.deepEqual(stays, ['a', 'c']);
  assert.deepEqual(r.moves.find((m) => m.id === 'b')!.kind, 'remove');
  const before = new Map(prev.placements.map((p) => [p.id, p]));
  for (const p of r.placements) {
    assert.deepEqual(p, before.get(p.id));
  }
});

test('adding an item moves nobody when free space remains', () => {
  const items: ItemSpec[] = [
    { id: 'a', width: 8, height: 8 },
    { id: 'b', width: 8, height: 8 },
  ];
  const prev = snapshotOf(items);
  const r = planUpdate(prev, { add: [{ id: 'c', width: 6, height: 6 }] });
  assert.ok(r.ok);
  assert.equal(r.moves.filter((m) => m.kind === 'move').length, 0);
  assert.equal(r.moves.find((m) => m.id === 'c')!.kind, 'add');
  const all = [...items, { id: 'c', width: 6, height: 6 }];
  const v = verifyAll(cfg.pageWidth, cfg.pageHeight, cfg.spacing, all, r.placements);
  assert.ok(v.ok, v.errors.join(';'));
});

test('resize relocates only the resized item', () => {
  const items: ItemSpec[] = [
    { id: 'a', width: 6, height: 6 },
    { id: 'b', width: 6, height: 6 },
    { id: 'c', width: 6, height: 6 },
  ];
  const prev = snapshotOf(items);
  const r = planUpdate(prev, { resize: [{ id: 'b', width: 10, height: 10 }] });
  assert.ok(r.ok);
  const movedIds = r.moves.filter((m) => m.kind === 'move').map((m) => m.id);
  assert.deepEqual(movedIds, ['b']);
  const next = r.placements.find((p) => p.id === 'b')!;
  assert.equal(next.width, 10);
  const all = items.map((i) => (i.id === 'b' ? { ...i, width: 10, height: 10 } : i));
  const v = verifyAll(cfg.pageWidth, cfg.pageHeight, cfg.spacing, all, r.placements);
  assert.ok(v.ok, v.errors.join(';'));
});

test('fixed items never move, even when a newcomer overlaps their spot', () => {
  const items: ItemSpec[] = [
    { id: 'pin', width: 10, height: 10, fixed: { page: 0, x: 0, y: 0 } },
    { id: 'free', width: 6, height: 6 },
  ];
  const prev = snapshotOf(items);
  const r = planUpdate(prev, { add: [{ id: 'big', width: 12, height: 12 }] });
  assert.ok(r.ok);
  const pin = r.placements.find((p) => p.id === 'pin')!;
  assert.deepEqual([pin.page, pin.x, pin.y], [0, 0, 0]);
  const all = [...items, { id: 'big', width: 12, height: 12 }];
  const v = verifyAll(cfg.pageWidth, cfg.pageHeight, cfg.spacing, all, r.placements);
  assert.ok(v.ok, v.errors.join(';'));
});

test('oversized resize fails explicitly and never publishes overlap', () => {
  const items: ItemSpec[] = [{ id: 'a', width: 6, height: 6 }];
  const prev = snapshotOf(items);
  const r = planUpdate(prev, { resize: [{ id: 'a', width: 64, height: 64 }] });
  assert.ok(r.ok); // layout itself is consistent
  assert.deepEqual(r.unpacked, [{ id: 'a', reason: 'too-large' }]);
  assert.equal(r.placements.find((p) => p.id === 'a'), undefined);
});

test('duplicate add id is rejected atomically with the old layout intact', () => {
  const items: ItemSpec[] = [{ id: 'a', width: 6, height: 6 }];
  const prev = snapshotOf(items);
  const r = planUpdate(prev, { add: [{ id: 'a', width: 9, height: 9 }] });
  assert.equal(r.ok, false);
  assert.deepEqual(r.placements, prev.placements);
  assert.match(r.error!, /conflicts/);
});

test('move plan is consistent: from matches previous layout, to matches new', () => {
  const items: ItemSpec[] = [
    { id: 'a', width: 6, height: 6 },
    { id: 'b', width: 6, height: 6 },
  ];
  const prev = snapshotOf(items);
  const r = planUpdate(prev, {
    add: [{ id: 'c', width: 20, height: 20 }],
    resize: [{ id: 'a', width: 8, height: 8 }],
  });
  assert.ok(r.ok);
  const before = new Map(prev.placements.map((p) => [p.id, p]));
  const after = new Map(r.placements.map((p) => [p.id, p]));
  for (const m of r.moves) {
    if (m.kind === 'add') assert.equal(m.from, null);
    else assert.deepEqual(m.from, before.get(m.id));
    if (m.kind === 'remove') assert.equal(m.to, null);
    else assert.deepEqual(m.to, after.get(m.id));
  }
});
