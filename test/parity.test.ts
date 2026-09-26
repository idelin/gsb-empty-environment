import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Server } from '../src/server.ts';
import { ListClient } from '../src/client.ts';
import { KeyboardDriver } from '../src/input/keyboard.ts';
import { MouseDriver } from '../src/input/mouse.ts';
import { pointForLocation } from '../src/layout.ts';
import { formatTree } from '../src/tree.ts';
import { makeTree, METRICS } from '../src/fixtures.ts';
import type { Location, TraceEntry } from '../src/types.ts';

function setup() {
  const server = new Server(makeTree());
  const client = new ListClient(server);
  return { server, client };
}

const targetsOf = (trace: TraceEntry[]) =>
  trace.filter((e) => e.op === 'target') as Array<{ op: 'target'; parentId: string; index: number }>;

test('keyboard and mouse produce identical logical op traces and outcomes', () => {
  const goal: Location = { parentId: 'veggies', index: 1 };

  // --- keyboard run: focus apple, lift it, step to the goal, commit ---
  const k = setup();
  const kb = new KeyboardDriver(k.client);
  kb.key('ArrowDown'); // focus fruits
  kb.key('ArrowDown'); // focus apple
  kb.key('Enter');     // lift apple
  for (let i = 0; i < 60; i++) {
    const t = k.client.machine.currentTarget;
    if (t && t.parentId === goal.parentId && t.index === goal.index) break;
    kb.key('ArrowDown');
  }
  assert.deepEqual(k.client.machine.currentTarget, goal);
  // preview must not touch the canonical tree
  assert.equal(formatTree(k.client.tree), formatTree(makeTree()));
  assert.notEqual(formatTree(k.client.machine.previewTree()), formatTree(makeTree()));
  kb.key('Enter'); // commit
  const kTrace = k.client.machine.trace;

  // --- mouse run: grab apple, visit the same targets, drop ---
  const m = setup();
  const md = new MouseDriver(m.client, METRICS);
  md.pointerDown(17, 30); // apple row center
  assert.equal(m.client.machine.phase, 'dragging');
  for (const t of targetsOf(kTrace)) {
    const p = pointForLocation(m.client.tree, METRICS, { parentId: t.parentId, index: t.index }, 'apple');
    md.pointerMove(p.x, p.y);
  }
  md.pointerUp();
  const mTrace = m.client.machine.trace;

  // identical logical operation traces
  assert.deepEqual(mTrace, kTrace);
  // identical final tree, focus and selection
  assert.equal(formatTree(m.client.tree), formatTree(k.client.tree));
  assert.deepEqual(m.client.machine.ui, k.client.machine.ui);
  assert.deepEqual(k.client.machine.ui, { focusId: 'apple', selection: ['apple'] });
  assert.deepEqual(k.client.tree.nodes.veggies.children, ['carrot', 'apple', 'daikon']);
});

test('keyboard promote and mouse drag to the same spot yield the same patch', () => {
  // keyboard: lift plantain, ArrowLeft promotes it next to its parent
  const k = setup();
  const kb = new KeyboardDriver(k.client);
  for (let i = 0; i < 4; i++) kb.key('ArrowDown'); // focus plantain
  kb.key('Enter');
  kb.key('ArrowLeft'); // promote: fruits[2], right after banana
  kb.key('Enter');
  const kCommit = k.client.machine.trace.find((e) => e.op === 'commit');
  assert.ok(kCommit && kCommit.op === 'commit');

  // mouse: drag plantain to the coordinates of the same location
  const m = setup();
  const md = new MouseDriver(m.client, METRICS);
  md.pointerDown(33, 70); // plantain row
  const p = pointForLocation(m.client.tree, METRICS, { parentId: 'fruits', index: 2 }, 'plantain');
  md.pointerMove(p.x, p.y);
  md.pointerUp();
  const mCommit = m.client.machine.trace.find((e) => e.op === 'commit');
  assert.ok(mCommit && mCommit.op === 'commit');

  assert.deepEqual(mCommit.patch, kCommit.patch);
  assert.deepEqual(kCommit.patch.to, { parentId: 'fruits', index: 2 });
  assert.equal(formatTree(m.client.tree), formatTree(k.client.tree));
});

test('cancel restores everything and never modifies the list', () => {
  const preset = { focusId: 'apple', selection: ['apple', 'cherry'] };
  const initial = formatTree(makeTree());

  // keyboard cancel via Escape
  const k = setup();
  k.client.machine.ui = { ...preset };
  const kb = new KeyboardDriver(k.client);
  kb.key('Enter');     // lift apple (focused)
  kb.key('ArrowDown'); // move the preview target
  kb.key('Escape');

  // mouse cancel via the driver-level cancel (drop outside / Esc)
  const m = setup();
  m.client.machine.ui = { ...preset };
  const md = new MouseDriver(m.client, METRICS);
  md.pointerDown(17, 30);
  md.pointerMove(16, 6 * 20 + 1);
  md.cancel();

  for (const c of [k, m]) {
    assert.equal(formatTree(c.client.tree), initial); // list unchanged
    assert.equal(c.client.version, 0);
    assert.equal(c.server.version, 0); // nothing reached the server
    assert.deepEqual(c.client.machine.ui, preset); // focus + selection restored
    const trace = c.client.machine.trace;
    assert.equal(trace[0].op, 'start');
    assert.equal(trace[trace.length - 1].op, 'cancel');
    assert.ok(!trace.some((e) => e.op === 'commit'));
  }
});

test('dropping into an own descendant is rejected by both drivers', () => {
  // keyboard: lift fruits, try to demote under its own child banana
  const k = setup();
  k.client.machine.ui = { focusId: 'fruits', selection: ['fruits'] };
  const kb = new KeyboardDriver(k.client);
  kb.key('Enter');
  const before = k.client.machine.trace.length;
  assert.equal(k.client.dragTo({ parentId: 'banana', index: 0 }), false);
  assert.equal(k.client.machine.trace.length, before); // no trace entry
  kb.key('Escape');

  // mouse: hover inside the fruits subtree at child depth
  const m = setup();
  const md = new MouseDriver(m.client, METRICS);
  md.pointerDown(1, 1); // grab fruits
  const traceLen = m.client.machine.trace.length;
  md.pointerMove(32, 3 * 20 + 1); // over plantain's slot, depth 2
  assert.deepEqual(m.client.machine.currentTarget, { parentId: 'root', index: 0 }); // unchanged
  assert.equal(m.client.machine.trace.length, traceLen); // rejected targets leave no trace
  md.cancel();

  assert.equal(formatTree(k.client.tree), formatTree(makeTree()));
  assert.equal(formatTree(m.client.tree), formatTree(makeTree()));
});
