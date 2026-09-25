import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildWorld } from '../fixtures/workbench.fixture.ts';

test('打开弹层后焦点限制在顶层，背景 inert', () => {
  const { tree, manager, mountLayerA } = buildWorld();
  mountLayerA();
  manager.openLayer({ id: 'A', rootId: 'layer-a', triggerId: 'btn-open-a' });

  assert.equal(manager.focusedNode(), 'layer-a-open-b'); // 层内第一个可聚焦节点
  assert.ok(manager.isInteractive('layer-a-open-b'));
  assert.ok(manager.isInteractive('layer-a-close'));
  assert.ok(!manager.isInteractive('btn-open-a')); // 背景不可交互
  assert.ok(tree.isInert('btn-open-a'));
  assert.equal(tree.inertClaimCount('app-root'), 1);
});

test('嵌套：内层打开后外层也 inert；关闭内层不解除外层对背景的隔离', () => {
  const { tree, manager, mountLayerA, mountLayerB } = buildWorld();
  mountLayerA();
  manager.openLayer({ id: 'A', rootId: 'layer-a', triggerId: 'btn-open-a' });
  mountLayerB();
  manager.openLayer({ id: 'B', rootId: 'layer-b', triggerId: 'layer-a-open-b' });

  assert.equal(manager.focusedNode(), 'layer-b-close');
  assert.ok(!manager.isInteractive('layer-a-open-b')); // 外层被内层隔离
  assert.equal(tree.inertClaimCount('app-root'), 2); // A、B 各持一份
  assert.equal(tree.inertClaimCount('layer-a'), 1);

  assert.ok(manager.handleKey('Escape')); // 关闭顶层 B
  manager.flush();
  assert.equal(manager.focusedNode(), 'layer-a-open-b'); // 恢复触发者
  assert.ok(tree.isInert('btn-open-a')); // 外层对背景的隔离仍在
  assert.equal(tree.inertClaimCount('app-root'), 1);
  assert.equal(tree.inertClaimCount('layer-a'), 0);
  assert.ok(manager.isInteractive('layer-a-close'));
});

test('退出动画存活期间可交互范围明确，finishExit 后卸载', () => {
  const { tree, manager, mountLayerA, mountLayerB } = buildWorld();
  mountLayerA();
  manager.openLayer({ id: 'A', rootId: 'layer-a', triggerId: 'btn-open-a' });
  mountLayerB();
  manager.openLayer({ id: 'B', rootId: 'layer-b', triggerId: 'layer-a-open-b' });

  manager.closeLayer('B');
  assert.deepEqual(manager.exitingLayerIds(), ['B']);
  assert.ok(tree.has('layer-b')); // 动画存活，DOM 仍在
  assert.ok(!manager.isInteractive('layer-b-close')); // 但不可交互
  assert.ok(!manager.isInteractive('layer-b-noop'));
  assert.ok(manager.isInteractive('layer-a-open-b')); // 新顶层可交互

  assert.ok(manager.finishExit('B'));
  assert.ok(!tree.has('layer-b'));
  assert.deepEqual(manager.exitingLayerIds(), []);
});

test('触发者已删除时回到合法后备（新顶层内第一个可聚焦节点）', () => {
  const { tree, manager, mountLayerA, mountLayerB } = buildWorld();
  mountLayerA();
  manager.openLayer({ id: 'A', rootId: 'layer-a', triggerId: 'btn-open-a' });
  mountLayerB();
  manager.openLayer({ id: 'B', rootId: 'layer-b', triggerId: 'layer-a-open-b' });

  tree.unmount('layer-a-open-b'); // 触发者被删除
  manager.handleKey('Escape');
  manager.flush();
  assert.equal(manager.focusedNode(), 'layer-a-close'); // A 内剩余的第一个可聚焦节点
});

test('栈空时触发者已删除，退回全局后备节点', () => {
  const { tree, manager, mountLayerA } = buildWorld();
  mountLayerA();
  manager.openLayer({ id: 'A', rootId: 'layer-a', triggerId: 'btn-open-a' });
  tree.unmount('btn-open-a');
  manager.closeLayer('A');
  manager.flush();
  assert.equal(manager.focusedNode(), 'btn-fallback');
});

test('焦点恢复允许异步挂载：目标后挂载时请求保持待决并在挂载后落地', () => {
  const { tree, manager, mountLayerA } = buildWorld();
  mountLayerA();
  manager.openLayer({ id: 'A', rootId: 'layer-a', triggerId: 'btn-open-a' });

  // 删除所有候选：触发者与全局后备都不存在
  tree.unmount('btn-open-a');
  tree.unmount('btn-fallback');
  manager.closeLayer('A');
  manager.flush();
  assert.equal(manager.focusedNode(), null); // 无处可去，不滞留非法区域
  assert.ok(manager.hasPendingFocus());

  // 触发者异步重新挂载 → 待决请求被满足
  tree.mount('main', { id: 'btn-open-a', focusable: true }, { 'btn-open-a': { width: 120, height: 36 } });
  assert.equal(manager.focusedNode(), 'btn-open-a');
  assert.ok(!manager.hasPendingFocus());
});

test('分代提交：交错的旧恢复请求不得夺焦', () => {
  const { tree, manager, mountLayerA, mountLayerB } = buildWorld();
  mountLayerA();
  manager.openLayer({ id: 'A', rootId: 'layer-a', triggerId: 'btn-open-a' });

  // 让恢复请求无法立即满足：触发者与后备都缺席
  tree.unmount('btn-open-a');
  tree.unmount('btn-fallback');
  manager.closeLayer('A'); // 代际 N：恢复请求待决
  const epochAfterClose = manager.currentEpoch();
  assert.ok(manager.hasPendingFocus());

  // 同一拍内新弹层打开 → 代际 N+1，焦点进入新层
  mountLayerB();
  manager.openLayer({ id: 'B', rootId: 'layer-b', triggerId: 'btn-nav-home' });
  assert.ok(manager.currentEpoch() > epochAfterClose);
  assert.equal(manager.focusedNode(), 'layer-b-close');

  // 旧目标异步重新挂载 + 排空队列：旧请求不得夺焦
  tree.mount('main', { id: 'btn-open-a', focusable: true }, { 'btn-open-a': { width: 120, height: 36 } });
  manager.flush();
  assert.equal(manager.focusedNode(), 'layer-b-close');
  assert.ok(!manager.isInteractive('btn-open-a')); // 仍被 B 隔离
});

test('Escape 只关闭可关闭的顶层', () => {
  const { manager, mountLayerA, mountLayerB } = buildWorld();
  mountLayerA();
  manager.openLayer({ id: 'A', rootId: 'layer-a', triggerId: 'btn-open-a' });
  mountLayerB();
  manager.openLayer({ id: 'B', rootId: 'layer-b', triggerId: 'layer-a-open-b', closable: false });

  assert.equal(manager.handleKey('Escape'), false); // 顶层不可关闭
  assert.deepEqual(manager.openLayerIds(), ['A', 'B']);

  manager.closeLayer('B'); // 程序化关闭仍可行
  manager.finishExit('B');
  manager.flush();
  assert.equal(manager.handleKey('Escape'), true); // 现在顶层是可关闭的 A
  assert.deepEqual(manager.openLayerIds(), []);
});

test('重复关闭与重复 finishExit 幂等', () => {
  const { manager, mountLayerA } = buildWorld();
  mountLayerA();
  manager.openLayer({ id: 'A', rootId: 'layer-a', triggerId: 'btn-open-a' });

  assert.equal(manager.closeLayer('A'), true);
  const snapshot = JSON.stringify(manager.debugState());
  assert.equal(manager.closeLayer('A'), false);
  assert.equal(manager.closeLayer('A'), false);
  assert.equal(manager.closeLayer('ghost'), false);
  assert.equal(JSON.stringify(manager.debugState()), snapshot); // 状态零变化

  assert.equal(manager.finishExit('A'), true);
  assert.equal(manager.finishExit('A'), false);
  assert.equal(manager.finishExit('ghost'), false);
});

test('焦点所在节点被卸载时重定向到顶层内合法目标', () => {
  const { tree, manager, mountLayerA } = buildWorld();
  mountLayerA();
  manager.openLayer({ id: 'A', rootId: 'layer-a', triggerId: 'btn-open-a' });
  assert.equal(manager.focusedNode(), 'layer-a-open-b');
  tree.unmount('layer-a-open-b'); // 卸载当前焦点节点
  manager.flush();
  assert.equal(manager.focusedNode(), 'layer-a-close');
});

test('全部关闭并结束后：inert 引用与待决焦点全部释放', () => {
  const { tree, manager, mountLayerA, mountLayerB } = buildWorld();
  mountLayerA();
  manager.openLayer({ id: 'A', rootId: 'layer-a', triggerId: 'btn-open-a' });
  mountLayerB();
  manager.openLayer({ id: 'B', rootId: 'layer-b', triggerId: 'layer-a-open-b' });

  manager.closeLayer('B');
  manager.closeLayer('A');
  manager.finishExit('B');
  manager.finishExit('A');
  manager.flush();

  assert.equal(tree.totalInertClaims(), 0);
  assert.equal(manager.hasPendingFocus(), false);
  assert.equal(manager.focusedNode(), 'btn-open-a');
  assert.ok(manager.isInteractive('btn-open-a')); // 背景恢复可交互
});
