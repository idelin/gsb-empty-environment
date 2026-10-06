import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { FocusTree } from '../src/index.ts';
import type { VNodeSpec } from '../src/index.ts';

describe('tab order rules', () => {
  it('orders positive tabindex by value, then document order', () => {
    const tree = FocusTree.fromSpec({
      id: 'root',
      children: [
        { id: 'a', tabindex: 2 },
        { id: 'b', tabindex: 1 },
        { id: 'c', tabindex: 2 },
        { id: 'd', tabindex: 1 },
      ],
    });
    assert.deepEqual(tree.tabOrder(), ['b', 'd', 'a', 'c']);
  });

  it('places zero tabindex and natively focusable nodes after positives, in document order', () => {
    const tree = FocusTree.fromSpec({
      id: 'root',
      children: [
        { id: 'zero1', tabindex: 0 },
        { id: 'pos', tabindex: 5 },
        { id: 'native', focusable: true },
        { id: 'plain' },
        { id: 'zero2', tabindex: 0 },
      ],
    });
    assert.deepEqual(tree.tabOrder(), ['pos', 'zero1', 'native', 'zero2']);
  });

  it('keeps negative tabindex out of tab order but programmatically focusable', () => {
    const tree = FocusTree.fromSpec({
      id: 'root',
      children: [
        { id: 'modal', tabindex: -1 },
        { id: 'btn', focusable: true },
        { id: 'skiptarget', tabindex: -2 },
      ],
    });
    assert.deepEqual(tree.tabOrder(), ['btn']);
    assert.deepEqual(tree.programmaticOrder(), ['modal', 'skiptarget']);
    assert.equal(tree.isFocusable('modal'), true);
    assert.equal(tree.isSequentiallyFocusable('modal'), false);
  });

  it('ignores nodes that are not focus-eligible by nature', () => {
    const tree = FocusTree.fromSpec({
      id: 'root',
      children: [{ id: 'div' }, { id: 'span', children: [{ id: 'text' }] }],
    });
    assert.deepEqual(tree.tabOrder(), []);
    assert.equal(tree.isFocusable('div'), false);
  });
});

describe('hidden ancestors', () => {
  it('makes the whole subtree unfocusable, including positive tabindex', () => {
    const tree = FocusTree.fromSpec({
      id: 'root',
      children: [
        {
          id: 'panel',
          hidden: true,
          children: [
            { id: 'inner-pos', tabindex: 1 },
            { id: 'inner-native', focusable: true },
          ],
        },
        { id: 'visible', tabindex: 0 },
      ],
    });
    assert.deepEqual(tree.tabOrder(), ['visible']);
    assert.deepEqual(tree.programmaticOrder(), []);
    const info = tree.info('inner-pos');
    assert.equal(info.effectivelyHidden, true);
    assert.equal(info.focusable, false);
  });

  it('does not leak hidden state to siblings', () => {
    const tree = FocusTree.fromSpec({
      id: 'root',
      children: [
        { id: 'gone', hidden: true, focusable: true },
        { id: 'here', focusable: true },
      ],
    });
    assert.deepEqual(tree.tabOrder(), ['here']);
  });
});

describe('disabled groups with exemptions', () => {
  const spec: VNodeSpec = {
    id: 'root',
    children: [
      {
        id: 'fieldset',
        group: true,
        disabled: true,
        exempt: ['legend'],
        children: [
          { id: 'legend', children: [{ id: 'legend-btn', focusable: true }] },
          { id: 'field-a', focusable: true },
          { id: 'field-b', tabindex: 3 },
        ],
      },
      { id: 'outside', focusable: true },
    ],
  };

  it('blocks descendants of a disabled group except exempt subtrees', () => {
    const tree = FocusTree.fromSpec(spec);
    assert.deepEqual(tree.tabOrder(), ['legend-btn', 'outside']);
    assert.equal(tree.info('field-a').effectivelyDisabled, true);
    assert.equal(tree.info('field-b').effectivelyDisabled, true);
    assert.equal(tree.info('legend-btn').effectivelyDisabled, false);
  });

  it('treats a disabled non-group node as only disabling itself', () => {
    const tree = FocusTree.fromSpec({
      id: 'root',
      children: [
        {
          id: 'btn',
          disabled: true,
          focusable: true,
          children: [{ id: 'child', focusable: true }],
        },
      ],
    });
    assert.deepEqual(tree.tabOrder(), ['child']);
    assert.equal(tree.isFocusable('btn'), false);
  });

  it('does not let an outer exemption bypass an inner disabled group', () => {
    const tree = FocusTree.fromSpec({
      id: 'root',
      children: [
        {
          id: 'outer',
          group: true,
          disabled: true,
          exempt: ['middle'],
          children: [
            {
              id: 'middle',
              children: [
                {
                  id: 'inner',
                  group: true,
                  disabled: true,
                  children: [{ id: 'deep', focusable: true }],
                },
                { id: 'sibling', focusable: true },
              ],
            },
          ],
        },
      ],
    });
    assert.deepEqual(tree.tabOrder(), ['sibling']);
    assert.equal(tree.info('deep').effectivelyDisabled, true);
  });

  it('enables the group again when disabled is lifted dynamically', () => {
    const tree = FocusTree.fromSpec(spec);
    tree.update('fieldset', { disabled: false });
    assert.deepEqual(tree.tabOrder(), ['field-b', 'legend-btn', 'field-a', 'outside']);
  });
});

describe('spec validation', () => {
  it('rejects duplicate ids at construction', () => {
    assert.throws(
      () =>
        FocusTree.fromSpec({
          id: 'root',
          children: [{ id: 'x' }, { id: 'y', children: [{ id: 'x' }] }],
        }),
      { name: 'DuplicateIdError' },
    );
  });

  it('rejects non-integer tabindex', () => {
    assert.throws(
      () => FocusTree.fromSpec({ id: 'root', children: [{ id: 'a', tabindex: 0.5 }] }).tabOrder(),
      { name: 'InvalidSpecError' },
    );
  });
});
