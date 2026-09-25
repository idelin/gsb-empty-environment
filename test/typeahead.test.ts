import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeController } from '../src/controller.ts';
import type { NodeData } from '../src/types.ts';

const base: NodeData[] = [
  {
    id: 'alpha', label: 'alpha',
    children: [
      { id: 'a1', label: 'a1' },
      { id: 'beta2', label: 'beta two', disabled: true },
    ],
  },
  { id: 'beta', label: 'beta', children: [{ id: 'b1', label: 'beta one' }] },
  { id: 'gamma', label: 'gamma' },
];

function makeController(): TreeController {
  const c = new TreeController(base, { now: () => 1000, typeaheadTimeout: 100 });
  c.expand('alpha');
  c.expand('beta');
  return c;
}

test('prefix match jumps focus', () => {
  const c = makeController();
  c.home();
  assert.equal(c.typeChar('b'), 'beta'); // enabled 'beta' beats disabled 'beta two'
  assert.equal(c.focus.id, 'beta');
  c.escape(); // end the first search, otherwise the buffer becomes 'bg'
  assert.equal(c.typeChar('g'), 'gamma');
});

test('a disabled node is never matched, even by its full label', () => {
  const c = makeController();
  c.home();
  let last: string | null = null;
  for (const ch of 'beta two') last = c.typeChar(ch);
  assert.equal(last, null); // 'beta two' is disabled: its unique match is skipped
  assert.notEqual(c.focus.id, 'beta2');
});

test('buffer accumulates within timeout and restarts after timeout', () => {
  let t = 1000;
  const c = new TreeController(base, { now: () => t, typeaheadTimeout: 100 });
  c.expand('alpha');
  c.expand('beta');
  c.typeChar('g');
  assert.equal(c.typeahead.currentBuffer, 'g');
  t = 1050;
  c.typeChar('a');
  assert.equal(c.typeahead.currentBuffer, 'ga');
  assert.equal(c.focus.id, 'gamma');
  t = 1200; // past timeout
  c.typeChar('b');
  assert.equal(c.typeahead.currentBuffer, 'b');
  assert.equal(c.focus.id, 'beta'); // fresh search wraps to first enabled 'b...'
});

test('search wraps around from after the current focus', () => {
  let t = 1000;
  const c = new TreeController(base, { now: () => t, typeaheadTimeout: 100 });
  c.expand('alpha');
  c.expand('beta');
  c.click('gamma');
  c.typeChar('a');
  assert.equal(c.focus.id, 'alpha');
});

test('buffer is bound to the tree version: any mutation clears it', () => {
  let t = 1000;
  const c = new TreeController(base, { now: () => t, typeaheadTimeout: 1000 });
  c.expand('alpha');
  c.expand('beta');
  c.typeChar('g');
  assert.equal(c.typeahead.currentBuffer, 'g');
  c.collapse('alpha'); // mutation
  assert.equal(c.typeahead.currentBuffer, '');
  c.typeChar('b');
  assert.equal(c.typeahead.currentBuffer, 'b');
});

test('Escape cancels the type-ahead buffer', () => {
  const c = new TreeController(base, { now: () => 1000, typeaheadTimeout: 1000 });
  c.expand('alpha');
  c.typeChar('a');
  c.escape();
  assert.equal(c.typeahead.currentBuffer, '');
});
