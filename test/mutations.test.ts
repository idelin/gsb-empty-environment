import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fullCompute } from '../src/compute.ts';
import { AccNameEngine } from '../src/engine.ts';
import type { GlyphMetrics, TreeNode } from '../src/types.ts';
import { metrics, node } from './fixtures.ts';

function assertMatchesFullReference(
  engine: AccNameEngine,
  root: TreeNode,
  glyphMetrics: GlyphMetrics,
  ids: string[],
): void {
  const reference = fullCompute(root, glyphMetrics);
  for (const id of ids) {
    assert.equal(engine.getName(id), reference.get(id)?.name, `name of ${id}`);
    assert.equal(
      engine.getDescription(id),
      reference.get(id)?.description,
      `description of ${id}`,
    );
  }
}

test('duplicate ids resolve to first occurrence; removing it shifts resolution', () => {
  const root = node({
    id: 'root',
    children: [
      node({ id: 'consumer', role: 'button', attrs: { 'aria-labelledby': 'dup' } }),
      node({ id: 'dup', text: 'First' }),
      node({ id: 'wrap', children: [node({ id: 'dup', text: 'Second' })] }),
    ],
  });
  const engine = new AccNameEngine(root, metrics);
  assert.equal(engine.getName('consumer'), 'First');
  assertMatchesFullReference(engine, root, metrics, ['consumer', 'dup']);

  engine.removeNode('dup'); // removes the first occurrence in document order
  assert.equal(engine.getName('consumer'), 'Second');
  assertMatchesFullReference(engine, root, metrics, ['consumer', 'dup']);
});

test('removing a labelledby target falls back to aria-label', () => {
  const root = node({
    id: 'root',
    children: [
      node({
        id: 't',
        role: 'button',
        attrs: { 'aria-labelledby': 'src', 'aria-label': 'Fallback' },
      }),
      node({ id: 'src', text: 'Source' }),
    ],
  });
  const engine = new AccNameEngine(root, metrics);
  assert.equal(engine.getName('t'), 'Source');

  engine.removeNode('src');
  assert.equal(engine.getName('t'), 'Fallback');
  assertMatchesFullReference(engine, root, metrics, ['t']);
});

test('role change to a name-prohibited role empties the name', () => {
  const root = node({
    id: 'root',
    children: [node({ id: 'logo', role: 'img', attrs: { alt: 'Logo' } })],
  });
  const engine = new AccNameEngine(root, metrics);
  assert.equal(engine.getName('logo'), 'Logo');

  engine.setRole('logo', 'presentation');
  assert.equal(engine.getName('logo'), '');
  assertMatchesFullReference(engine, root, metrics, ['logo']);

  engine.setRole('logo', 'img');
  assert.equal(engine.getName('logo'), 'Logo');
  assertMatchesFullReference(engine, root, metrics, ['logo']);
});

test('nested controls contribute their alternative text', () => {
  const root = node({
    id: 'root',
    children: [
      node({
        id: 'btn',
        role: 'button',
        children: [
          node({ id: 'icon', role: 'img', attrs: { alt: 'Save' } }),
          node({ id: 'caption', text: 'document' }),
        ],
      }),
      node({ id: 'lb', role: 'label', attrs: { for: 'inp' }, text: 'Your name' }),
      node({ id: 'inp', role: 'textbox', attrs: { value: 'Ada' } }),
    ],
  });
  const engine = new AccNameEngine(root, metrics);
  assert.equal(engine.getName('btn'), 'Save document');
  assert.equal(engine.getName('inp'), 'Your name');
  assertMatchesFullReference(engine, root, metrics, ['btn', 'inp', 'icon', 'lb']);

  engine.setAttribute('icon', 'alt', 'Store');
  assert.equal(engine.getName('btn'), 'Store document');
  assertMatchesFullReference(engine, root, metrics, ['btn', 'inp']);
});
