/**
 * 运行样例：嵌套弹层焦点管理全链路演示。
 * 运行：node samples/demo.ts   （Node ≥ 23.6，原生类型擦除）
 */

import { buildWorld } from '../fixtures/workbench.fixture.ts';
import type { LayerManager } from '../src/layer-manager.ts';
import type { NodeTree } from '../src/tree.ts';

function show(manager: LayerManager, label: string): void {
  const s = manager.debugState();
  console.log(`\n● ${label}`);
  console.log(
    `  焦点=${s.focused ?? '(无)'}  打开=[${s.open.join(', ')}]  退出中=[${s.exiting.join(', ')}]` +
      `  inert引用=${s.inertClaims}  待决焦点=${s.pendingFocus}  代=${s.epoch}`,
  );
}

function interactive(manager: LayerManager, ids: string[]): string {
  return ids.map((id) => `${id}:${manager.isInteractive(id) ? '可交互' : '隔离'}`).join('  ');
}

const { tree, manager, mountLayerA, mountLayerB } = buildWorld();

console.log('=== 嵌套弹层焦点管理 运行样例 ===');
show(manager, '初始：无弹层，背景可交互');
console.log(`  ${interactive(manager, ['btn-open-a', 'btn-fallback'])}`);

mountLayerA();
manager.openLayer({ id: 'A', rootId: 'layer-a', triggerId: 'btn-open-a' });
show(manager, '打开 A（触发者 btn-open-a）：焦点进入顶层，背景 inert');
console.log(`  ${interactive(manager, ['layer-a-open-b', 'btn-open-a'])}  inert(app-root)=${tree.inertClaimCount('app-root')}`);

mountLayerB();
manager.openLayer({ id: 'B', rootId: 'layer-b', triggerId: 'layer-a-open-b' });
show(manager, '打开 B（触发者 layer-a-open-b）：外层 A 也被隔离');
console.log(`  inert(app-root)=${tree.inertClaimCount('app-root')}  inert(layer-a)=${tree.inertClaimCount('layer-a')}`);

manager.handleKey('Escape');
manager.flush();
show(manager, 'Escape 关闭顶层 B：焦点恢复触发者，外层对背景的隔离不解除');
console.log(`  ${interactive(manager, ['layer-a-open-b', 'layer-b-close', 'btn-open-a'])}`);
console.log(`  inert(app-root)=${tree.inertClaimCount('app-root')}  inert(layer-a)=${tree.inertClaimCount('layer-a')}`);

manager.finishExit('B');
show(manager, 'B 退出动画结束：DOM 卸载');
console.log(`  layer-b 存在=${tree.has('layer-b')}`);

mountLayerB();
manager.openLayer({ id: 'B2', rootId: 'layer-b', triggerId: 'layer-a-open-b' });
tree.unmount('layer-a-open-b');
manager.handleKey('Escape');
manager.flush();
show(manager, '再次打开 B 后删除触发者，Escape：焦点落到合法后备（A 内剩余可聚焦节点）');
manager.finishExit('B2');

tree.unmount('btn-open-a');
tree.unmount('btn-fallback');
manager.closeLayer('A');
manager.flush();
show(manager, '关闭 A：触发者与全局后备都缺席 → 恢复请求待决，焦点不滞留非法区域');

tree.mount('main', { id: 'btn-open-a', focusable: true }, { 'btn-open-a': { width: 120, height: 36 } });
show(manager, '触发者异步重新挂载：待决请求被满足');
manager.finishExit('A');

mountLayerA();
manager.openLayer({ id: 'C', rootId: 'layer-a', triggerId: 'btn-open-a', closable: false });
const escaped = manager.handleKey('Escape');
show(manager, `打开不可关闭层 C：Escape=${escaped}（只关闭可关闭的顶层）`);
manager.closeLayer('C');
manager.finishExit('C');
manager.flush();
show(manager, '程序化关闭 C：焦点恢复 btn-open-a');

// 分代：旧恢复请求不得夺焦
tree.unmount('btn-open-a');
tree.unmount('btn-fallback');
mountLayerB();
manager.openLayer({ id: 'D', rootId: 'layer-b', triggerId: 'btn-open-a' });
manager.closeLayer('D'); // 恢复目标缺席 → 待决（代 N）
mountLayerA();
manager.openLayer({ id: 'E', rootId: 'layer-a', triggerId: 'btn-nav-home' }); // 代 N+1
tree.mount('main', { id: 'btn-open-a', focusable: true }, { 'btn-open-a': { width: 120, height: 36 } });
manager.flush();
show(manager, 'D 关闭的恢复请求待决时又打开 E，随后旧目标重新挂载：过时请求不得夺焦');
console.log(`  ${interactive(manager, ['layer-a-open-b', 'btn-open-a'])}`);

// 收尾：全部关闭，校验引用释放
manager.closeLayer('E');
manager.finishExit('D');
manager.finishExit('E');
tree.mount('footer', { id: 'btn-fallback', focusable: true }, { 'btn-fallback': { width: 96, height: 32 } });
manager.flush();
show(manager, '收尾：所有弹层关闭并结束退出');
console.log(`  inert引用全部释放=${tree.totalInertClaims() === 0}  待决焦点全部释放=${!manager.hasPendingFocus()}`);
