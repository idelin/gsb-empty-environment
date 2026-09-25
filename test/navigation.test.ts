import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeController } from '../src/controller.ts';
import type { NodeData } from '../src/types.ts';

function makeController(data: NodeData[]): TreeController {
  return new TreeController(data);
}

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

test('expand/collapse changes the visible rows', () => {
  const c = makeController(base);
  assert.deepEqual([...c.visible.ids], ['alpha', 'beta', 'gamma']);
  c.expand('alpha');
  assert.deepEqual([...c.visible.ids], ['alpha', 'a1', 'a2', 'a3', 'beta', 'gamma']);
  c.expand('a3');
  assert.deepEqual([...c.visible.ids], ['alpha', 'a1', 'a2', 'a3', 'a3x', 'beta', 'gamma']);
  c.collapse('alpha');
  assert.deepEqual([...c.visible.ids], ['alpha', 'beta', 'gamma']);
});

test('Down/Up/Home/End skip disabled nodes', () => {
  const c = makeController(base);
  c.expand('alpha');
  c.expand('gamma');
  c.home();
  assert.equal(c.focus.id, 'alpha');
  c.moveDown(); // a1
  c.moveDown(); // a2 disabled -> a3
  assert.equal(c.focus.id, 'a3');
  c.moveDown(); // beta disabled -> gamma
  assert.equal(c.focus.id, 'gamma');
  c.moveUp(); // a3
  assert.equal(c.focus.id, 'a3');
  c.end();
  assert.equal(c.focus.id, 'g1');
});

test('collapsing the subtree containing focus returns focus to the collapsed node', () => {
  const c = makeController(base);
  c.expand('alpha');
  c.expand('a3');
  c.click('a3x');
  c.collapse('alpha');
  assert.equal(c.focus.id, 'alpha');
});

test('collapsing a disabled ancestor returns focus to the nearest enabled ancestor', () => {
  const tree: NodeData[] = [
    { id: 'top', label: 'top', children: [
      { id: 'mid', label: 'mid', disabled: true, children: [{ id: 'leaf', label: 'leaf' }] },
    ] },
  ];
  const c = makeController(tree);
  c.expand('top');
  c.expand('mid');
  c.click('leaf');
  c.collapse('mid');
  assert.equal(c.focus.id, 'top');
});

test('ArrowRight expands, ArrowLeft collapses or moves focus to parent', () => {
  const c = makeController(base);
  c.home();
  c.handleKey('ArrowRight'); // expand focused alpha
  assert.ok(c.tree.isExpanded('alpha'));
  c.handleKey('ArrowLeft'); // focused node is expanded -> collapse it
  assert.ok(!c.tree.isExpanded('alpha'));
  assert.equal(c.focus.id, 'alpha');
  c.handleKey('ArrowRight');
  c.moveDown(); // a1
  c.handleKey('ArrowLeft'); // a1 is a leaf -> focus its parent
  assert.equal(c.focus.id, 'alpha');
  c.moveDown();
  c.moveDown(); // a3 (a2 disabled)
  assert.equal(c.focus.id, 'a3');
  c.handleKey('ArrowLeft'); // a3 collapsed -> focus parent, alpha stays expanded
  assert.equal(c.focus.id, 'alpha');
  assert.ok(c.tree.isExpanded('alpha'));
});

test('clicking or focusing a disabled node is ignored', () => {
  const c = makeController(base);
  c.home();
  c.click('beta');
  assert.equal(c.focus.id, 'alpha');
  c.click('beta', { shift: true });
  assert.equal(c.focus.id, 'alpha');
});
