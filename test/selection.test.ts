import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeController } from '../src/controller.ts';
import type { NodeData } from '../src/types.ts';

const base: NodeData[] = [
  {
    id: 'alpha', label: 'alpha',
    children: [
      { id: 'a1', label: 'a1' },
      { id: 'a2', label: 'a2', disabled: true },
      { id: 'a3', label: 'a3', children: [{ id: 'a3x', label: 'a3x' }] },
    ],
  },
  { id: 'beta', label: 'beta', disabled: true, children: [{ id: 'b1', label: 'b1' }] },
  { id: 'gamma', label: 'gamma', children: [{ id: 'g1', label: 'g1' }] },
];

test('focus and selection are independent', () => {
  const c = new TreeController(base);
  c.expand('alpha');
  c.click('a1');
  assert.equal(c.focus.id, 'a1');
  c.home();
  assert.equal(c.focus.id, 'alpha');
  assert.deepEqual([...c.selection.selected], ['a1']); // moving focus keeps selection
});

test('Space toggles the focused node; disabled nodes cannot be selected', () => {
  const c = new TreeController(base);
  c.expand('alpha');
  c.home();
  c.handleKey(' ');
  assert.ok(c.selection.isSelected('alpha'));
  assert.equal(c.toggleSelect('a2'), false);
  assert.ok(!c.selection.isSelected('a2'));
  c.toggleSelect('a2'); // again, still no-op
  assert.equal(c.selection.anchor, 'alpha'); // anchor never becomes a2
});

test('Shift range follows visible order and excludes disabled nodes', () => {
  const c = new TreeController(base);
  c.expand('alpha');
  c.expand('a3');
  c.expand('gamma');
  c.click('a1');
  c.click('g1', { shift: true });
  assert.deepEqual(
    [...c.selection.selected].sort(),
    ['a1', 'a3', 'a3x', 'g1', 'gamma'], // a2 and beta excluded
  );
});

test('stale anchor (tree version changed) re-binds to the new focus', () => {
  const c = new TreeController(base);
  c.expand('alpha');
  c.click('a1'); // anchor = a1 at current version
  assert.ok(!c.selection.isAnchorStale());
  c.expand('a3'); // mutation -> anchor is now stale
  assert.ok(c.selection.isAnchorStale());
  c.click('g1', { shift: true });
  // Re-anchored at g1: the range is exactly {g1}.
  assert.deepEqual([...c.selection.selected], ['g1']);
  assert.equal(c.selection.anchor, 'g1');
});

test('removing nodes prunes selection and anchor', () => {
  const c = new TreeController(base);
  c.expand('alpha');
  c.click('a1');
  c.toggleSelect('g1'); // multi-select, anchor g1
  c.removeNode('alpha'); // removes a1 (selected) and subtree; focus was g1
  assert.deepEqual([...c.selection.selected], ['g1']);
  assert.equal(c.selection.anchor, 'g1');
  c.removeNode('g1'); // anchor removed
  assert.equal(c.selection.anchor, null);
});

test('inserting children (including async completion) does not reset selection', () => {
  const c = new TreeController(base);
  c.expand('alpha');
  c.click('a1');
  c.toggleSelect('g1');
  c.insertChildren('gamma', [{ id: 'g2', label: 'g2' }]);
  assert.deepEqual([...c.selection.selected].sort(), ['a1', 'g1']);
  assert.deepEqual([...c.tree.childrenOf('gamma')], ['g1', 'g2']);
});
