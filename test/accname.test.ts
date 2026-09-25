import { test } from 'node:test';
import assert from 'node:assert/strict';
import { NodeTree } from '../src/tree.ts';
import {
  computeAccessibleDescription,
  computeAccessibleName,
} from '../src/accname.ts';
import { fixtureMetrics } from './fixtures.ts';

const metrics = fixtureMetrics();

test('aria-labelledby 链跨多个节点且含环时按 visited 集合终止并回退', () => {
  const tree = new NodeTree([
    { key: 'btn', role: 'button', attrs: { 'aria-labelledby': 'n1' } },
    { key: 't1', id: 'n1', attrs: { 'aria-labelledby': 'n2' }, text: 'One' },
    { key: 't2', id: 'n2', attrs: { 'aria-labelledby': 'n1' }, text: 'Two' },
  ]);
  // btn → n1 → n2 → n1(已访问，成环) → 回退到 n2 自身文本 "Two"
  assert.equal(computeAccessibleName(tree, metrics, 'btn'), 'Two');
  // t1 作为根：→ n2 → n1(成环) → 回退到 n1 文本 "One"
  assert.equal(computeAccessibleName(tree, metrics, 't1'), 'Two');
  // t2 作为根：→ n1 → n2(成环) → 回退到 n2 文本 "Two"… 经 n1 链得 "Two"
  assert.equal(computeAccessibleName(tree, metrics, 't2'), 'One');
});

test('aria-labelledby 多级链取到最深节点的文本', () => {
  const tree = new NodeTree([
    { key: 'a', role: 'button', attrs: { 'aria-labelledby': 'l1' } },
    { key: 'n1', id: 'l1', attrs: { 'aria-labelledby': 'l2' }, text: 'Shallow' },
    { key: 'n2', id: 'l2', attrs: { 'aria-labelledby': 'l3' }, text: 'Middle' },
    { key: 'n3', id: 'l3', text: 'Deep' },
  ]);
  assert.equal(computeAccessibleName(tree, metrics, 'a'), 'Deep');
});

test('多个 IDREF 按序拼接，悬空引用贡献为空且不抛错', () => {
  const tree = new NodeTree([
    { key: 'a', role: 'button', attrs: { 'aria-labelledby': 'x missing y' }, text: 'Fallback' },
    { key: 'nx', id: 'x', text: 'Left' },
    { key: 'ny', id: 'y', text: 'Right' },
  ]);
  assert.equal(computeAccessibleName(tree, metrics, 'a'), 'Left Right');
});

test('隐藏但被显式引用的内容计入名称，普通隐藏内容跳过', () => {
  const tree = new NodeTree([
    { key: 'referenced', role: 'button', attrs: { 'aria-labelledby': 'hid' } },
    { key: 'secret', id: 'hid', hidden: true, text: 'Secret' },
    {
      key: 'container',
      role: 'button',
      children: [
        { key: 'ghost', hidden: true, text: 'Ghost' },
        { key: 'shown', text: 'Shown' },
      ],
    },
  ]);
  assert.equal(computeAccessibleName(tree, metrics, 'referenced'), 'Secret');
  assert.equal(computeAccessibleName(tree, metrics, 'container'), 'Shown');
});

test('零宽字形按夹具度量从渲染文本中剔除', () => {
  const tree = new NodeTree([
    { key: 'a', role: 'button', text: 'A​b­c' },
  ]);
  assert.equal(computeAccessibleName(tree, metrics, 'a'), 'Abc');
});

test('禁止命名的角色在根节点得空名，被引用时仍贡献文本', () => {
  const tree = new NodeTree([
    { key: 'p', role: 'paragraph', text: 'Nope' },
    { key: 'pres', role: 'presentation', text: 'Decorative' },
    { key: 'btn', role: 'button', attrs: { 'aria-labelledby': 'ref1' } },
    { key: 'para', id: 'ref1', role: 'paragraph', text: 'Still used' },
  ]);
  assert.equal(computeAccessibleName(tree, metrics, 'p'), '');
  assert.equal(computeAccessibleName(tree, metrics, 'pres'), '');
  assert.equal(computeAccessibleName(tree, metrics, 'btn'), 'Still used');
});

test('名称来源优先级：labelledby > aria-label > 原生标签 > 内容 > title', () => {
  const tree = new NodeTree([
    { key: 'ref', id: 'r', text: 'FromRef' },
    {
      key: 'n1', role: 'button', text: 'Content',
      attrs: { 'aria-labelledby': 'r', 'aria-label': 'AriaLabel', title: 'T' },
    },
    { key: 'n2', role: 'button', text: 'Content', attrs: { 'aria-label': 'AriaLabel', title: 'T' } },
    { key: 'n3', role: 'img', attrs: { alt: 'AltText', 'aria-label': 'AriaLabel' } },
    { key: 'n4', role: 'button', text: 'Content', attrs: { title: 'T' } },
    { key: 'n5', role: 'img', attrs: { title: 'JustTitle' } },
  ]);
  assert.equal(computeAccessibleName(tree, metrics, 'n1'), 'FromRef');
  assert.equal(computeAccessibleName(tree, metrics, 'n2'), 'AriaLabel');
  assert.equal(computeAccessibleName(tree, metrics, 'n3'), 'AriaLabel');
  assert.equal(computeAccessibleName(tree, metrics, 'n4'), 'Content');
  assert.equal(computeAccessibleName(tree, metrics, 'n5'), 'JustTitle');
});

test('原生标签：label[for] 关联与嵌套控件替代文本', () => {
  const tree = new NodeTree([
    { key: 'input', role: 'textbox', id: 'email' },
    { key: 'lbl', role: 'label', attrs: { for: 'email' }, text: 'Email address' },
    {
      key: 'btn',
      role: 'button',
      children: [
        { key: 'icon', role: 'img', attrs: { alt: 'Save icon' } },
        { key: 'caption', text: 'Save' },
      ],
    },
  ]);
  assert.equal(computeAccessibleName(tree, metrics, 'input'), 'Email address');
  assert.equal(computeAccessibleName(tree, metrics, 'btn'), 'Save icon Save');
});

test('描述优先级：describedby > aria-description > title，链含环时终止', () => {
  const tree = new NodeTree([
    {
      key: 'a', role: 'button', text: 'X',
      attrs: {
        'aria-describedby': 'd1',
        'aria-description': 'Direct',
        title: 'Title',
      },
    },
    { key: 'n1', id: 'd1', attrs: { 'aria-describedby': 'd2' }, text: 'First' },
    { key: 'n2', id: 'd2', attrs: { 'aria-describedby': 'd1' }, text: 'Second' },
    { key: 'b', role: 'button', text: 'Y', attrs: { 'aria-description': 'Direct', title: 'Title' } },
    { key: 'c', role: 'button', text: 'Z', attrs: { title: 'Title' } },
  ]);
  // a → d1 → d2 → d1(成环) → 回退到 d2 文本 "Second"
  assert.equal(computeAccessibleDescription(tree, metrics, 'a'), 'Second');
  assert.equal(computeAccessibleDescription(tree, metrics, 'b'), 'Direct');
  assert.equal(computeAccessibleDescription(tree, metrics, 'c'), 'Title');
});
