import { test } from 'node:test';
import { replay } from './replay.ts';

test('关闭子层回到触发节点', () => {
  replay([
    { op: 'base', id: 'root' },
    { op: 'node', id: 'btn-a', layer: 'root' },
    { op: 'node', id: 'btn-b', layer: 'root' },
    { op: 'open', id: 'menu', trigger: 'btn-b' },
    { op: 'node', id: 'mi-1', layer: 'menu' },
    { op: 'node', id: 'mi-2', layer: 'menu' },
    { op: 'expectFocus', id: 'mi-1' }, // 开层聚焦首项
    { op: 'close', id: 'menu' },
    { op: 'expectFocus', id: 'btn-b' }, // 回到触发节点
    { op: 'expectStack', ids: ['root'] },
  ]);
});

test('禁用节点：触发节点被禁用则找最近可聚焦祖先', () => {
  replay([
    { op: 'base', id: 'root' },
    { op: 'node', id: 'toolbar', layer: 'root' },
    { op: 'node', id: 'btn-save', layer: 'root', parent: 'toolbar' },
    { op: 'open', id: 'dialog', trigger: 'btn-save' },
    { op: 'node', id: 'field', layer: 'dialog' },
    { op: 'disable', id: 'btn-save' }, // 弹层打开期间触发节点被禁用
    { op: 'close', id: 'dialog' },
    { op: 'expectFocus', id: 'toolbar' }, // 跳过禁用节点，落到祖先
  ]);
});

test('禁用节点：首项被禁用则开层聚焦下一项', () => {
  replay([
    { op: 'base', id: 'root' },
    { op: 'node', id: 'btn', layer: 'root' },
    { op: 'open', id: 'menu', trigger: 'btn' },
    { op: 'node', id: 'mi-1', layer: 'menu', disabled: true },
    { op: 'node', id: 'mi-2', layer: 'menu' },
    { op: 'expectFocus', id: 'mi-2' },
  ]);
});

test('跨层删除：触发节点被删则找祖先，祖先也没了退到父层首项', () => {
  replay([
    { op: 'base', id: 'root' },
    { op: 'node', id: 'logo', layer: 'root' },
    { op: 'node', id: 'panel', layer: 'root' },
    { op: 'node', id: 'btn-del', layer: 'root', parent: 'panel' },
    { op: 'open', id: 'confirm', trigger: 'btn-del' },
    { op: 'node', id: 'ok', layer: 'confirm' },
    { op: 'remove', id: 'btn-del' }, // 触发节点被另一层逻辑删除
    { op: 'close', id: 'confirm' },
    { op: 'expectFocus', id: 'panel' }, // 退化到最近可聚焦祖先

    // 再开一层，这次连祖先也被删
    { op: 'open', id: 'confirm2', trigger: 'panel' },
    { op: 'node', id: 'ok2', layer: 'confirm2' },
    { op: 'remove', id: 'panel' },
    { op: 'close', id: 'confirm2' },
    { op: 'expectFocus', id: 'logo' }, // 祖先链全灭，退到父层首项
  ]);
});

test('跨层删除：被删节点正持焦时立即转移', () => {
  replay([
    { op: 'base', id: 'root' },
    { op: 'node', id: 'keep', layer: 'root' },
    { op: 'node', id: 'group', layer: 'root' },
    { op: 'node', id: 'hot', layer: 'root', parent: 'group' },
    { op: 'open', id: 'pop', trigger: 'keep' },
    { op: 'node', id: 'x', layer: 'pop' },
    { op: 'close', id: 'pop' },
    { op: 'expectFocus', id: 'keep' },
    { op: 'remove', id: 'keep' }, // 删除当前聚焦节点
    { op: 'expectFocus', id: 'group' }, // 无祖先，退到本层下一可聚焦项
  ]);
});

test('异步关闭：旧层恢复不得抢走新层焦点', () => {
  replay([
    { op: 'base', id: 'root' },
    { op: 'node', id: 't1', layer: 'root' },
    { op: 'node', id: 't2', layer: 'root' },
    { op: 'open', id: 'old', trigger: 't1' },
    { op: 'node', id: 'old-item', layer: 'old' },
    { op: 'expectFocus', id: 'old-item' },
    { op: 'close', id: 'old', defer: true }, // 动画中，恢复被挂起
    { op: 'open', id: 'fresh', trigger: 't2' }, // 用户立刻开了新层
    { op: 'node', id: 'fresh-item', layer: 'fresh' },
    { op: 'expectFocus', id: 'fresh-item' },
    { op: 'flush' }, // 旧层的延迟恢复到点
    { op: 'expectFocus', id: 'fresh-item' }, // 不抢焦点
    { op: 'expectStack', ids: ['root', 'fresh'] },
  ]);
});

test('异步关闭：无干扰时正常恢复', () => {
  replay([
    { op: 'base', id: 'root' },
    { op: 'node', id: 't1', layer: 'root' },
    { op: 'open', id: 'pop', trigger: 't1' },
    { op: 'node', id: 'item', layer: 'pop' },
    { op: 'close', id: 'pop', defer: true },
    { op: 'expectFocus', id: 'item' }, // 恢复尚未发生
    { op: 'flush' },
    { op: 'expectFocus', id: 't1' },
  ]);
});

test('异步关闭：连续关两层，只有最新恢复生效', () => {
  replay([
    { op: 'base', id: 'root' },
    { op: 'node', id: 't1', layer: 'root' },
    { op: 'open', id: 'a', trigger: 't1' },
    { op: 'node', id: 'a-item', layer: 'a' },
    { op: 'open', id: 'b', trigger: 'a-item' },
    { op: 'node', id: 'b-item', layer: 'b' },
    { op: 'close', id: 'b', defer: true },
    { op: 'close', id: 'a', defer: true },
    { op: 'flush' },
    { op: 'expectFocus', id: 't1' }, // b 的恢复作废，a 的恢复生效
    { op: 'expectStack', ids: ['root'] },
  ]);
});

test('重入打开：焦点回到触发节点时同步再开新层', () => {
  let armed = false; // wizard-1 打开后才武装，避免注册 t1 时的初始聚焦误触发
  replay(
    [
      { op: 'base', id: 'root' },
      { op: 'node', id: 't1', layer: 'root' },
      { op: 'open', id: 'wizard-1', trigger: 't1' },
      { op: 'node', id: 'next', layer: 'wizard-1' },
      { op: 'close', id: 'wizard-1' }, // 恢复焦点到 t1，监听器重入打开 wizard-2
      { op: 'expectFocus', id: 'step2-field' }, // 最终焦点在新层首项
      { op: 'expectStack', ids: ['root', 'wizard-2'] },
    ],
    {
      onFocus(id, fm) {
        if (fm.stackIds.includes('wizard-1')) {
          armed = true;
          return;
        }
        if (armed && id === 't1' && !fm.stackIds.includes('wizard-2')) {
          fm.openLayer('wizard-2', 't1');
          fm.registerNode('step2-field', 'wizard-2'); // 注册即自动聚焦
        }
      },
    },
  );
});

test('重入打开：关闭回调里打开替换层，旧恢复被丢弃', () => {
  replay([
    { op: 'base', id: 'root' },
    { op: 'node', id: 't1', layer: 'root' },
    { op: 'open', id: 'a', trigger: 't1' },
    { op: 'node', id: 'a-item', layer: 'a' },
    { op: 'close', id: 'a', defer: true },
    // 在 a 的恢复执行前，同步链路又开了 b 并立即同步关掉 a 的兄弟场景：
    { op: 'open', id: 'b', trigger: 't1' },
    { op: 'node', id: 'b-item', layer: 'b' },
    { op: 'close', id: 'b' }, // 同步关闭，焦点回 t1
    { op: 'expectFocus', id: 't1' },
    { op: 'flush' }, // a 的延迟恢复到点，但 epoch 已变
    { op: 'expectFocus', id: 't1' },
    { op: 'expectStack', ids: ['root'] },
  ]);
});

test('深层栈：关闭顶层回到中间层触发节点', () => {
  replay([
    { op: 'base', id: 'root' },
    { op: 'node', id: 't1', layer: 'root' },
    { op: 'open', id: 'l1', trigger: 't1' },
    { op: 'node', id: 'l1-item', layer: 'l1' },
    { op: 'open', id: 'l2', trigger: 'l1-item' },
    { op: 'node', id: 'l2-item', layer: 'l2' },
    { op: 'expectFocus', id: 'l2-item' },
    { op: 'close', id: 'l2' },
    { op: 'expectFocus', id: 'l1-item' },
    { op: 'close', id: 'l1' },
    { op: 'expectFocus', id: 't1' },
  ]);
});
