// 事件重放测试：驱动 FocusManager 的脚本化场景，断言完整事件流。
// 覆盖：禁用节点、跨层删除、重入打开、异步关闭不抢新层焦点。
import { importTs } from '../scripts/strip-types.mjs';
import { test, run, assertDeepEqual, assertEqual, deferred } from './harness.mjs';

const { FocusManager } = await importTs(new URL('../src/focus-manager.ts', import.meta.url));

const leaf = (id, extra = {}) => ({ id, ...extra });
const branch = (id, children, extra = {}) => ({ id, children, ...extra });

const baseLayer = () => ({
  id: 'base',
  root: branch('app', [leaf('btn1'), leaf('btn2')], { focusable: false }),
});

const menuLayer = (triggerId) => ({
  id: 'menu',
  triggerId,
  root: branch('mroot', [leaf('mi1'), leaf('mi2')], { focusable: false }),
});

const dialogLayer = (triggerId) => ({
  id: 'dialog',
  triggerId,
  root: branch('droot', [leaf('d1')], { focusable: false }),
});

test('关闭子层回到触发节点', async () => {
  const m = new FocusManager();
  m.openLayer(baseLayer());
  m.openLayer(menuLayer('btn2'));
  await m.closeLayer('menu');
  assertEqual(m.focused, 'btn2', 'focus returns to trigger');
  assertDeepEqual(m.stack, ['base'], 'stack');
  assertDeepEqual(m.log, [
    { type: 'layer:opened', layerId: 'base' },
    { type: 'focus:change', from: null, to: 'btn1', reason: 'layer-open' },
    { type: 'layer:opened', layerId: 'menu' },
    { type: 'focus:change', from: 'btn1', to: 'mi1', reason: 'layer-open' },
    { type: 'layer:closing', layerId: 'menu' },
    { type: 'layer:closed', layerId: 'menu' },
    { type: 'focus:change', from: 'mi1', to: 'btn2', reason: 'restore' },
  ], 'event log');
});

test('禁用节点：跳过、拒绝聚焦、禁用后转移、禁用触发节点回退', async () => {
  const m = new FocusManager();
  m.openLayer({
    id: 'base',
    root: branch('app', [leaf('btn1', { disabled: true }), leaf('btn2'), leaf('btn3')], { focusable: false }),
  });
  assertEqual(m.focused, 'btn2', 'open skips disabled first item');
  assertEqual(m.focus('btn1'), false, 'disabled node rejects focus');
  assertEqual(m.focus('nope'), false, 'unknown node rejects focus');
  assertEqual(m.focus('btn3'), true, 'api focus');
  m.setDisabled('btn3', true);
  assertEqual(m.focused, 'btn2', 'disabling focused node refocuses');
  m.setDisabled('btn1', false);
  m.openLayer({ id: 'menu', triggerId: 'btn2', root: branch('mroot', [leaf('mi1')], { focusable: false }) });
  m.setDisabled('btn2', true);
  await m.closeLayer('menu');
  assertEqual(m.focused, 'btn1', 'disabled trigger falls back to parent layer first item');
  assertDeepEqual(m.log, [
    { type: 'layer:opened', layerId: 'base' },
    { type: 'focus:change', from: null, to: 'btn2', reason: 'layer-open' },
    { type: 'focus:change', from: 'btn2', to: 'btn3', reason: 'api' },
    { type: 'node:disabled', id: 'btn3', disabled: true },
    { type: 'focus:change', from: 'btn3', to: 'btn2', reason: 'node-disabled' },
    { type: 'node:disabled', id: 'btn1', disabled: false },
    { type: 'layer:opened', layerId: 'menu' },
    { type: 'focus:change', from: 'btn2', to: 'mi1', reason: 'layer-open' },
    { type: 'node:disabled', id: 'btn2', disabled: true },
    { type: 'layer:closing', layerId: 'menu' },
    { type: 'layer:closed', layerId: 'menu' },
    { type: 'focus:change', from: 'mi1', to: 'btn1', reason: 'restore' },
  ], 'event log');
});

test('跨层删除：触发节点已删则找祖先，再退父层首项', async () => {
  const m = new FocusManager();
  m.openLayer({
    id: 'base',
    root: branch('app', [
      branch('toolbar', [leaf('open-menu-btn'), leaf('sibling-btn')]),
      leaf('first-btn'),
    ], { focusable: false }),
  });
  // 阶段一：删除触发节点，祖先生效
  m.openLayer({ id: 'menu', triggerId: 'open-menu-btn', root: branch('mroot', [leaf('mi1')], { focusable: false }) });
  m.removeNode('open-menu-btn');
  await m.closeLayer('menu');
  assertEqual(m.focused, 'toolbar', 'deleted trigger falls back to nearest focusable ancestor');
  // 阶段二：祖先也不可用，退到父层首项
  m.openLayer(dialogLayer('first-btn'));
  m.setDisabled('toolbar', true);
  m.removeNode('first-btn');
  await m.closeLayer('dialog');
  assertEqual(m.focused, 'sibling-btn', 'no focusable ancestor falls back to parent layer first item');
  assertDeepEqual(m.log, [
    { type: 'layer:opened', layerId: 'base' },
    { type: 'focus:change', from: null, to: 'toolbar', reason: 'layer-open' },
    { type: 'layer:opened', layerId: 'menu' },
    { type: 'focus:change', from: 'toolbar', to: 'mi1', reason: 'layer-open' },
    { type: 'node:removed', id: 'open-menu-btn' },
    { type: 'layer:closing', layerId: 'menu' },
    { type: 'layer:closed', layerId: 'menu' },
    { type: 'focus:change', from: 'mi1', to: 'toolbar', reason: 'restore' },
    { type: 'layer:opened', layerId: 'dialog' },
    { type: 'focus:change', from: 'toolbar', to: 'd1', reason: 'layer-open' },
    { type: 'node:disabled', id: 'toolbar', disabled: true },
    { type: 'node:removed', id: 'first-btn' },
    { type: 'layer:closing', layerId: 'dialog' },
    { type: 'layer:closed', layerId: 'dialog' },
    { type: 'focus:change', from: 'd1', to: 'sibling-btn', reason: 'restore' },
  ], 'event log');
});

test('异步关闭：过渡结束后才恢复焦点', async () => {
  const gate = deferred();
  const m = new FocusManager({ transition: () => gate.promise });
  m.openLayer(baseLayer());
  m.openLayer(menuLayer('btn2'));
  const closing = m.closeLayer('menu');
  assertEqual(m.focused, 'mi1', 'focus not restored before transition ends');
  assertDeepEqual(m.stack, ['base', 'menu'], 'layer still on stack while closing');
  gate.resolve();
  await closing;
  assertEqual(m.focused, 'btn2', 'focus restored after transition');
  assertDeepEqual(m.stack, ['base'], 'stack');
  assertDeepEqual(m.log, [
    { type: 'layer:opened', layerId: 'base' },
    { type: 'focus:change', from: null, to: 'btn1', reason: 'layer-open' },
    { type: 'layer:opened', layerId: 'menu' },
    { type: 'focus:change', from: 'btn1', to: 'mi1', reason: 'layer-open' },
    { type: 'layer:closing', layerId: 'menu' },
    { type: 'layer:closed', layerId: 'menu' },
    { type: 'focus:change', from: 'mi1', to: 'btn2', reason: 'restore' },
  ], 'event log');
});

test('异步关闭旧层不能抢走新层焦点', async () => {
  const gate = deferred();
  const m = new FocusManager({ transition: () => gate.promise });
  m.openLayer(baseLayer());
  m.openLayer(menuLayer('btn2'));
  const closing = m.closeLayer('menu');
  m.openLayer(dialogLayer('btn1'));
  assertEqual(m.focused, 'd1', 'new layer takes focus while old close is pending');
  gate.resolve();
  await closing;
  assertEqual(m.focused, 'd1', 'stale async close does not steal focus');
  assertDeepEqual(m.stack, ['base', 'dialog'], 'old layer removed, new layer kept');
  assertDeepEqual(m.log, [
    { type: 'layer:opened', layerId: 'base' },
    { type: 'focus:change', from: null, to: 'btn1', reason: 'layer-open' },
    { type: 'layer:opened', layerId: 'menu' },
    { type: 'focus:change', from: 'btn1', to: 'mi1', reason: 'layer-open' },
    { type: 'layer:closing', layerId: 'menu' },
    { type: 'layer:opened', layerId: 'dialog' },
    { type: 'focus:change', from: 'mi1', to: 'd1', reason: 'layer-open' },
    { type: 'layer:closed', layerId: 'menu' },
  ], 'event log has no focus steal after layer:closed');
});

test('重入打开：closing 回调中打开新层', async () => {
  const m = new FocusManager();
  m.openLayer(baseLayer());
  m.openLayer(menuLayer('btn2'));
  m.onEvent((event) => {
    if (event.type === 'layer:closing' && event.layerId === 'menu') {
      m.openLayer(dialogLayer('btn1'));
    }
  });
  await m.closeLayer('menu');
  assertEqual(m.focused, 'd1', 'reentrant open wins, stale restore skipped');
  assertDeepEqual(m.stack, ['base', 'dialog'], 'stack');
  assertDeepEqual(m.log, [
    { type: 'layer:opened', layerId: 'base' },
    { type: 'focus:change', from: null, to: 'btn1', reason: 'layer-open' },
    { type: 'layer:opened', layerId: 'menu' },
    { type: 'focus:change', from: 'btn1', to: 'mi1', reason: 'layer-open' },
    { type: 'layer:closing', layerId: 'menu' },
    { type: 'layer:opened', layerId: 'dialog' },
    { type: 'focus:change', from: 'mi1', to: 'd1', reason: 'layer-open' },
    { type: 'layer:closed', layerId: 'menu' },
  ], 'event log');
});

test('重入打开：closed 回调中打开新层', async () => {
  const m = new FocusManager();
  m.openLayer(baseLayer());
  m.openLayer(menuLayer('btn2'));
  m.onEvent((event) => {
    if (event.type === 'layer:closed' && event.layerId === 'menu') {
      m.openLayer(dialogLayer('btn1'));
    }
  });
  await m.closeLayer('menu');
  assertEqual(m.focused, 'd1', 'reentrant open on closed wins');
  assertDeepEqual(m.stack, ['base', 'dialog'], 'stack');
  assertDeepEqual(m.log, [
    { type: 'layer:opened', layerId: 'base' },
    { type: 'focus:change', from: null, to: 'btn1', reason: 'layer-open' },
    { type: 'layer:opened', layerId: 'menu' },
    { type: 'focus:change', from: 'btn1', to: 'mi1', reason: 'layer-open' },
    { type: 'layer:closing', layerId: 'menu' },
    { type: 'layer:closed', layerId: 'menu' },
    { type: 'layer:opened', layerId: 'dialog' },
    { type: 'focus:change', from: 'mi1', to: 'd1', reason: 'layer-open' },
  ], 'event log');
});

test('级联关闭：关闭父层同时关闭其子层', async () => {
  const m = new FocusManager();
  m.openLayer(baseLayer());
  m.openLayer(menuLayer('btn2'));
  m.openLayer({ id: 'sub', triggerId: 'mi1', root: branch('sroot', [leaf('s1')], { focusable: false }) });
  await m.closeLayer('menu');
  assertDeepEqual(m.stack, ['base'], 'child layers cascade-closed');
  assertEqual(m.focused, 'btn2', 'focus returns to the closing layer trigger');
  assertDeepEqual(m.log, [
    { type: 'layer:opened', layerId: 'base' },
    { type: 'focus:change', from: null, to: 'btn1', reason: 'layer-open' },
    { type: 'layer:opened', layerId: 'menu' },
    { type: 'focus:change', from: 'btn1', to: 'mi1', reason: 'layer-open' },
    { type: 'layer:opened', layerId: 'sub' },
    { type: 'focus:change', from: 'mi1', to: 's1', reason: 'layer-open' },
    { type: 'layer:closing', layerId: 'sub' },
    { type: 'layer:closing', layerId: 'menu' },
    { type: 'layer:closed', layerId: 'sub' },
    { type: 'layer:closed', layerId: 'menu' },
    { type: 'focus:change', from: 's1', to: 'btn2', reason: 'restore' },
  ], 'event log');
});

// 事件重放：把场景记录为命令脚本，执行两次，事件流必须逐条一致且符合黄金日志。
async function runScript(script) {
  const m = new FocusManager();
  const pending = [];
  for (const cmd of script) {
    if (cmd.do === 'open') m.openLayer(cmd.layer);
    else if (cmd.do === 'close') pending.push(m.closeLayer(cmd.layer));
    else if (cmd.do === 'remove') m.removeNode(cmd.id);
    else if (cmd.do === 'disable') m.setDisabled(cmd.id, cmd.disabled);
    else if (cmd.do === 'focus') m.focus(cmd.id);
    else if (cmd.do === 'flush') await Promise.all(pending.splice(0));
    else throw new Error(`unknown command: ${cmd.do}`);
  }
  await Promise.all(pending);
  return m;
}

test('事件重放：禁用节点 + 跨层删除 + 触发回退，重放结果确定', async () => {
  const script = [
    { do: 'open', layer: {
      id: 'base',
      root: branch('app', [leaf('btn1', { disabled: true }), leaf('btn2'), leaf('btn3')], { focusable: false }),
    } },
    { do: 'open', layer: {
      id: 'menu',
      triggerId: 'btn3',
      root: branch('mroot', [leaf('mi1'), leaf('mi2', { disabled: true }), leaf('mi3')], { focusable: false }),
    } },
    { do: 'focus', id: 'mi3' },
    { do: 'focus', id: 'mi2' },
    { do: 'remove', id: 'mi3' },
    { do: 'disable', id: 'btn3', disabled: true },
    { do: 'close', layer: 'menu' },
    { do: 'flush' },
  ];
  const golden = [
    { type: 'layer:opened', layerId: 'base' },
    { type: 'focus:change', from: null, to: 'btn2', reason: 'layer-open' },
    { type: 'layer:opened', layerId: 'menu' },
    { type: 'focus:change', from: 'btn2', to: 'mi1', reason: 'layer-open' },
    { type: 'focus:change', from: 'mi1', to: 'mi3', reason: 'api' },
    { type: 'node:removed', id: 'mi3' },
    { type: 'focus:change', from: 'mi3', to: 'mi1', reason: 'node-removed' },
    { type: 'node:disabled', id: 'btn3', disabled: true },
    { type: 'layer:closing', layerId: 'menu' },
    { type: 'layer:closed', layerId: 'menu' },
    { type: 'focus:change', from: 'mi1', to: 'btn2', reason: 'restore' },
  ];
  const first = await runScript(script);
  assertDeepEqual(first.log, golden, 'golden event log');
  assertEqual(first.focused, 'btn2', 'final focus');
  assertDeepEqual(first.stack, ['base'], 'final stack');
  const replayed = await runScript(script);
  assertDeepEqual(replayed.log, first.log, 'replay produces identical event stream');
  assertEqual(replayed.focused, first.focused, 'replay produces identical final focus');
});

await run();
