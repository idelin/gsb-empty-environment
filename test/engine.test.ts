import { test } from 'node:test';
import assert from 'node:assert/strict';
import { signal, h, t, attr, mount, applyPatches, renderToString } from '../src/index.ts';

test('验收场景：user.name 变更 → 仅产出一条 UpdateTextNode 精准补丁', () => {
  const user = { name: signal('Alice') };
  const theme = signal('light'); // 干扰项：不相关的响应式状态

  const view = mount(
    h('div', { class: 'app', 'data-theme': attr(() => theme()) },
      h('header', null,
        h('h1', null, '用户面板'),
      ),
      h('main', null,
        // 目标节点：'欢迎，' + user.name
        h('p', { class: 'greeting' }, t(() => '欢迎，' + user.name())),
        h('p', { class: 'tip' }, '这是一段静态文本'),
        h('footer', null, h('span', null, t(() => `当前主题：${theme()}`))),
      ),
    ),
  );

  // 挂载后清空补丁与求值计数，只观察更新阶段
  view.patches.length = 0;
  const evalsBefore = view.stats.evaluations;

  // 找到目标文本节点 id（挂载后 '欢迎，Alice' 所在节点）
  const greeting = [...view.index.values()].find(
    (n) => n.type === 'text' && n.text === '欢迎，Alice',
  )!;
  assert.ok(greeting, '目标文本节点应已挂载');

  user.name.set('Bob');

  // 1. 只产出一条补丁，且就是 UpdateTextNode
  assert.equal(view.patches.length, 1);
  assert.deepEqual(view.patches[0], {
    op: 'UpdateTextNode',
    id: greeting.id,
    text: '欢迎，Bob',
  });

  // 2. 无全树 Diff：只重算了 1 个绑定（目标文本绑定），
  //    其余绑定（主题属性、主题文本）零求值、零遍历
  assert.equal(view.stats.evaluations - evalsBefore, 1);

  // 3. 应用补丁后界面局部对齐
  applyPatches(view.index, view.patches);
  assert.equal(
    renderToString(view.root),
    '<div class="app" data-theme="light">' +
      '<header><h1>用户面板</h1></header>' +
      '<main>' +
        '<p class="greeting">欢迎，Bob</p>' +
        '<p class="tip">这是一段静态文本</p>' +
        '<footer><span>当前主题：light</span></footer>' +
      '</main>' +
    '</div>',
  );
});

test('连续变更：影子值保证增量补丁始终正确（含回改场景）', () => {
  const name = signal('Alice');
  const view = mount(h('p', null, t(() => '欢迎，' + name())));
  view.patches.length = 0;

  name.set('Bob');
  name.set('Carol');
  name.set('Alice'); // 改回初值：若依赖节点当前值会漏发此补丁

  assert.deepEqual(view.patches, [
    { op: 'UpdateTextNode', id: 2, text: '欢迎，Bob' },
    { op: 'UpdateTextNode', id: 2, text: '欢迎，Carol' },
    { op: 'UpdateTextNode', id: 2, text: '欢迎，Alice' },
  ]);

  applyPatches(view.index, view.patches);
  assert.equal(renderToString(view.root), '<p>欢迎，Alice</p>');
});

test('相同值写入：set 同值或计算结果不变时不产出任何补丁', () => {
  const name = signal('Alice');
  const view = mount(h('p', null, t(() => '欢迎，' + name())));
  view.patches.length = 0;

  name.set('Alice'); // Object.is 相同，订阅者根本不会被通知
  assert.equal(view.patches.length, 0);
});

test('属性绑定：值变化产出 SetAttribute，变为 null 产出 RemoveAttribute', () => {
  const status = signal<string | null>('online');
  const view = mount(
    h('div', { 'data-status': attr(() => status()) }, 'x'),
  );
  view.patches.length = 0;
  const elId = view.root.id;

  status.set('busy');
  assert.deepEqual(view.patches, [
    { op: 'SetAttribute', id: elId, name: 'data-status', value: 'busy' },
  ]);

  status.set(null);
  assert.deepEqual(view.patches, [
    { op: 'SetAttribute', id: elId, name: 'data-status', value: 'busy' },
    { op: 'RemoveAttribute', id: elId, name: 'data-status' },
  ]);

  applyPatches(view.index, view.patches);
  assert.equal(renderToString(view.root), '<div>x</div>');
});

test('多绑定共享一个 Signal：一次变更精准命中全部依赖绑定，仍无树 Diff', () => {
  const name = signal('Alice');
  const unrelated = signal(0);
  const view = mount(
    h('div', null,
      h('p', null, t(() => '欢迎，' + name())),
      h('p', { title: attr(() => name()) }, t(() => `你好，${name()}`)),
      h('p', null, t(() => `计数：${unrelated()}`)),
    ),
  );
  view.patches.length = 0;
  const evalsBefore = view.stats.evaluations;

  name.set('Bob');

  // 3 个依赖 name 的绑定各产出 1 条补丁；unrelated 绑定完全不被波及
  assert.equal(view.patches.length, 3);
  assert.deepEqual(
    view.patches.map((p) => p.op),
    ['UpdateTextNode', 'SetAttribute', 'UpdateTextNode'],
  );
  assert.equal(view.stats.evaluations - evalsBefore, 3);
});

test('静态子树零开销：静态文本与属性永不参与更新', () => {
  const name = signal('Alice');
  const view = mount(
    h('div', { class: 'static' },
      h('p', null, '纯静态'),
      h('p', null, t(() => name())),
    ),
  );
  view.patches.length = 0;
  const evalsBefore = view.stats.evaluations;

  name.set('Bob');

  assert.equal(view.stats.evaluations - evalsBefore, 1);
  assert.equal(view.patches.length, 1);
});
