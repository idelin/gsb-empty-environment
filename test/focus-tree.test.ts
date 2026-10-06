import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { FocusTree } from '../src/index.ts';
import type { UpdateAttrs, VNodeSpec } from '../src/index.ts';

function mulberry32(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomSpec(rand: () => number, prefix: string, depth: number): VNodeSpec {
  const node: VNodeSpec = { id: prefix };
  const roll = rand();
  if (roll < 0.35) node.tabindex = [-2, -1, 0, 1, 2, 3][Math.floor(rand() * 6)];
  else if (roll < 0.6) node.focusable = true;
  if (rand() < 0.15) node.hidden = true;
  if (rand() < 0.15) node.disabled = true;
  if (rand() < 0.2) node.group = true;
  if (depth > 0) {
    const count = Math.floor(rand() * 4);
    for (let i = 0; i < count; i += 1) {
      node.children ??= [];
      node.children.push(randomSpec(rand, `${prefix}.${i}`, depth - 1));
    }
  }
  if (node.group && node.children && rand() < 0.5) {
    node.exempt = [node.children[Math.floor(rand() * node.children.length)].id];
  }
  return node;
}

function collectIds(spec: VNodeSpec): string[] {
  const ids = [spec.id];
  for (const child of spec.children ?? []) ids.push(...collectIds(child));
  return ids;
}

describe('dynamic attribute updates', () => {
  it('reorders tab order when tabindex changes', () => {
    const tree = FocusTree.fromSpec({
      id: 'root',
      children: [
        { id: 'a', tabindex: 1 },
        { id: 'b', tabindex: 2 },
        { id: 'c', focusable: true },
      ],
    });
    assert.deepEqual(tree.tabOrder(), ['a', 'b', 'c']);
    tree.update('b', { tabindex: -1 });
    assert.deepEqual(tree.tabOrder(), ['a', 'c']);
    assert.deepEqual(tree.programmaticOrder(), ['b']);
    tree.update('b', { tabindex: null });
    assert.deepEqual(tree.tabOrder(), ['a', 'c']);
    assert.equal(tree.isFocusable('b'), false);
  });

  it('hiding an ancestor unfocuses descendants; unhiding restores them', () => {
    const tree = FocusTree.fromSpec({
      id: 'root',
      children: [
        {
          id: 'panel',
          children: [
            { id: 'p1', tabindex: 1 },
            { id: 'p2', focusable: true },
          ],
        },
        { id: 'tail', focusable: true },
      ],
    });
    assert.deepEqual(tree.tabOrder(), ['p1', 'p2', 'tail']);
    tree.update('panel', { hidden: true });
    assert.deepEqual(tree.tabOrder(), ['tail']);
    tree.update('panel', { hidden: false });
    assert.deepEqual(tree.tabOrder(), ['p1', 'p2', 'tail']);
  });

  it('matches full-tree expansion after every mutation (fuzz)', () => {
    const rand = mulberry32(20261006);
    for (let trial = 0; trial < 25; trial += 1) {
      const tree = FocusTree.fromSpec(randomSpec(rand, `t${trial}`, 3));
      for (let step = 0; step < 40; step += 1) {
        const ids = collectIds(tree.snapshot());
        const target = ids[Math.floor(rand() * ids.length)];
        const action = rand();
        if (action < 0.55) {
          const attrs: UpdateAttrs = {};
          const which = rand();
          if (which < 0.3) attrs.tabindex = [null, -1, 0, 1, 5][Math.floor(rand() * 5)];
          else if (which < 0.5) attrs.hidden = rand() < 0.5;
          else if (which < 0.7) attrs.disabled = rand() < 0.5;
          else if (which < 0.85) attrs.focusable = rand() < 0.5;
          else attrs.exempt = [ids[Math.floor(rand() * ids.length)]];
          tree.update(target, attrs);
        } else if (action < 0.75) {
          tree.insert(target, randomSpec(rand, `t${trial}.s${step}`, 1));
        } else if (target !== `t${trial}` && ids.length > 1) {
          tree.remove(target);
        }

        const rebuilt = FocusTree.fromSpec(tree.snapshot());
        assert.deepEqual(tree.expand(), rebuilt.expand(), `expand diverged at trial ${trial} step ${step}`);
        assert.deepEqual(tree.tabOrder(), rebuilt.tabOrder(), `tabOrder diverged at trial ${trial} step ${step}`);
        assert.deepEqual(
          tree.programmaticOrder(),
          rebuilt.programmaticOrder(),
          `programmaticOrder diverged at trial ${trial} step ${step}`,
        );
      }
    }
  });
});

describe('duplicate id rejection', () => {
  const base = (): FocusTree =>
    FocusTree.fromSpec({
      id: 'root',
      children: [{ id: 'a', children: [{ id: 'b' }] }, { id: 'c' }],
    });

  it('rejects insert of a subtree containing an existing id', () => {
    const tree = base();
    assert.throws(() => tree.insert('root', { id: 'b' }), { name: 'DuplicateIdError' });
    assert.throws(() => tree.insert('a', { id: 'new', children: [{ id: 'c' }] }), {
      name: 'DuplicateIdError',
    });
  });

  it('rejects duplicates inside the inserted subtree itself', () => {
    const tree = base();
    assert.throws(() => tree.insert('root', { id: 'n', children: [{ id: 'm' }, { id: 'm' }] }), {
      name: 'DuplicateIdError',
    });
  });

  it('rejects renaming a node to an existing id', () => {
    const tree = base();
    assert.throws(() => tree.update('b', { id: 'c' }), { name: 'DuplicateIdError' });
    tree.update('b', { id: 'b2' });
    assert.equal(tree.info('b2').id, 'b2');
    assert.throws(() => tree.info('b'), { name: 'UnknownNodeError' });
  });

  it('leaves the tree unchanged after a rejected mutation', () => {
    const tree = base();
    const before = tree.snapshot();
    assert.throws(() => tree.insert('root', { id: 'a' }));
    assert.throws(() => tree.update('c', { id: 'b' }));
    assert.deepEqual(tree.snapshot(), before);
  });
});

describe('structural operations', () => {
  it('inserts at a document-order position and removes subtrees', () => {
    const tree = FocusTree.fromSpec({
      id: 'root',
      children: [
        { id: 'first', focusable: true },
        { id: 'last', focusable: true },
      ],
    });
    tree.insert('root', { id: 'mid', focusable: true }, 1);
    assert.deepEqual(tree.tabOrder(), ['first', 'mid', 'last']);
    tree.remove('mid');
    assert.deepEqual(tree.tabOrder(), ['first', 'last']);
    assert.throws(() => tree.remove('root'), { name: 'InvalidSpecError' });
    assert.throws(() => tree.remove('nope'), { name: 'UnknownNodeError' });
    assert.throws(() => tree.update('nope', { hidden: true }), { name: 'UnknownNodeError' });
  });

  it('rejects children changes through update()', () => {
    const tree = FocusTree.fromSpec({ id: 'root', children: [{ id: 'a' }] });
    assert.throws(
      () => tree.update('a', { children: [{ id: 'b' }] } as unknown as UpdateAttrs),
      { name: 'InvalidSpecError' },
    );
  });
});
