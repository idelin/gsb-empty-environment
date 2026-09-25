/**
 * 状态探索：用确定性伪随机序列交错执行
 * 打开/关闭/重复关闭/Escape/结束退出/挂载/卸载/重挂载/排空队列，
 * 每一步校验不变量：
 *  1. 焦点（若存在）必须落在顶层弹层内且可交互 —— 任一时刻只有顶层拥有焦点；
 *  2. 退出动画存活的弹层内节点一律不可交互；
 *  3. 重复关闭是幂等无操作；
 *  4. 收尾后所有 inert 引用与待决焦点请求全部释放。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { NodeTree } from '../src/tree.ts';
import type { NodeMetrics, NodeSpec } from '../src/tree.ts';
import { LayerManager } from '../src/layer-manager.ts';
import type { OpenLayerOptions } from '../src/layer-manager.ts';
import { screenSpec, baseMetrics } from '../fixtures/workbench.fixture.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const FIXTURE_FOCUSABLES = [
  'btn-nav-home',
  'btn-open-a',
  'btn-open-b',
  'btn-ghost',
  'btn-hidden',
  'btn-fallback',
];

test('状态探索：任意交错下焦点只属于顶层，引用最终全部释放', () => {
  for (let seed = 1; seed <= 120; seed++) {
    const rand = mulberry32(seed);
    const tree = new NodeTree(screenSpec(), baseMetrics());
    const manager = new LayerManager(tree, { fallbackId: 'btn-fallback' });

    const liveIds: string[] = [];
    const collectLive = (id: string): void => {
      liveIds.push(id);
      for (const child of tree.childIdsOf(id)) collectLive(child);
    };
    collectLive('screen');

    const focusableSet = new Set<string>(FIXTURE_FOCUSABLES);
    const graveyard: string[] = []; // 被卸载的可聚焦节点，可异步重挂载
    const layerBtnIds = new Map<string, string[]>();
    const layerAllIds = new Map<string, string[]>();
    const layerIdByRoot = new Map<string, string>();
    const layerRootById = new Map<string, string>();
    let layerSeq = 0;
    let nodeSeq = 0;

    const subtreeIds = (id: string): string[] => {
      const out: string[] = [];
      const walk = (cur: string): void => {
        out.push(cur);
        for (const child of tree.childIdsOf(cur)) walk(child);
      };
      if (tree.has(id)) walk(id);
      return out;
    };

    const removeLive = (ids: string[]): void => {
      for (const id of ids) {
        const at = liveIds.indexOf(id);
        if (at !== -1) liveIds.splice(at, 1);
      }
    };

    const checkInvariants = (label: string): void => {
      const focused = manager.focusedNode();
      if (focused !== null) {
        assert.ok(tree.has(focused), `[seed ${seed}] ${label}: 焦点 ${focused} 必须存在`);
        assert.ok(manager.isInteractive(focused), `[seed ${seed}] ${label}: 焦点 ${focused} 必须可交互`);
        const top = manager.topLayer();
        if (top) {
          assert.ok(
            tree.contains(top.rootId, focused),
            `[seed ${seed}] ${label}: 焦点 ${focused} 必须在顶层 ${top.id} 内`,
          );
        }
      }
      for (const layerId of manager.exitingLayerIds()) {
        const rootId = layerRootById.get(layerId);
        if (!rootId) continue;
        for (const btn of layerBtnIds.get(rootId) ?? []) {
          // 只约束仍位于退出层子树内的节点（被重挂载到别处的 id 不受限）
          if (tree.has(btn) && tree.contains(rootId, btn)) {
            assert.ok(!manager.isInteractive(btn), `[seed ${seed}] ${label}: 退出中节点 ${btn} 不得可交互`);
          }
        }
      }
    };

    const pick = <T>(items: T[]): T => items[Math.floor(rand() * items.length)];

    const opOpen = (): void => {
      if (!tree.has('layer-host')) {
        tree.mount('screen', { id: 'layer-host' }, { 'layer-host': { width: 1280, height: 800 } });
        liveIds.push('layer-host');
      }
      const seq = layerSeq++;
      const rootId = `layer-${seq}`;
      const layerId = `L${seq}`;
      const childSpecs: NodeSpec[] = [];
      const metrics: Record<string, NodeMetrics> = { [rootId]: { width: 400, height: 300 } };
      const btnIds: string[] = [];
      const allIds: string[] = [rootId];
      const btnCount = Math.floor(rand() * 4);
      for (let i = 0; i < btnCount; i++) {
        const bid = `${rootId}-btn-${i}`;
        childSpecs.push({ id: bid, focusable: true });
        metrics[bid] = rand() < 0.2 ? { width: 0, height: 0 } : { width: 80, height: 30 };
        btnIds.push(bid);
        allIds.push(bid);
        focusableSet.add(bid);
      }
      tree.mount('layer-host', { id: rootId, children: childSpecs }, metrics);
      liveIds.push(...allIds);
      layerBtnIds.set(rootId, btnIds);
      layerAllIds.set(rootId, allIds);
      layerIdByRoot.set(rootId, layerId);
      layerRootById.set(layerId, rootId);
      const triggerPool: Array<string | undefined> = [...liveIds, 'ghost-trigger', undefined];
      const triggerId = pick(triggerPool);
      const options: OpenLayerOptions = { id: layerId, rootId, closable: rand() < 0.8 };
      if (triggerId !== undefined) options.triggerId = triggerId;
      if (rand() < 0.3) options.initialFocusId = rand() < 0.5 && btnIds.length > 0 ? btnIds[0] : 'ghost-initial';
      manager.openLayer(options);
    };

    const opCloseRandom = (): void => {
      const pool = [...manager.openLayerIds(), ...manager.exitingLayerIds(), 'ghost-layer'];
      manager.closeLayer(pick(pool));
    };

    const opDoubleClose = (): void => {
      const open = manager.openLayerIds();
      const id = open.length > 0 && rand() < 0.8 ? open[open.length - 1] : pick([...open, 'ghost-layer']);
      manager.closeLayer(id);
      const snapshot = JSON.stringify(manager.debugState());
      assert.equal(manager.closeLayer(id), false, `[seed ${seed}] 重复关闭必须返回 false`);
      assert.equal(
        JSON.stringify(manager.debugState()),
        snapshot,
        `[seed ${seed}] 重复关闭不得改变任何状态`,
      );
    };

    const opEscape = (): void => {
      manager.handleKey('Escape');
    };

    const opFinishExit = (): void => {
      const pool = [...manager.exitingLayerIds(), 'ghost-layer'];
      const id = pick(pool);
      const rootId = layerRootById.get(id);
      const before = rootId ? (layerAllIds.get(rootId) ?? []) : [];
      if (manager.finishExit(id) && rootId) {
        removeLive(before);
        for (const gone of before) {
          if (focusableSet.has(gone)) graveyard.push(gone);
        }
        layerIdByRoot.delete(rootId);
        layerRootById.delete(id);
      }
    };

    const opUnmount = (): void => {
      const candidates = liveIds.filter((id) => id !== 'screen');
      if (candidates.length === 0) return;
      const id = pick(candidates);
      const gone = subtreeIds(id);
      if (tree.unmount(id)) {
        removeLive(gone);
        for (const g of gone) {
          if (focusableSet.has(g)) graveyard.push(g);
        }
      }
    };

    const opMount = (): void => {
      const containers = ['app-root', 'header', 'main', 'footer', 'layer-host']
        .filter((id) => tree.has(id));
      for (const layerId of manager.openLayerIds()) {
        const rootId = layerRootById.get(layerId);
        if (rootId && tree.has(rootId)) containers.push(rootId);
      }
      if (containers.length === 0) return;
      const id = `extra-${nodeSeq++}`;
      const parent = pick(containers);
      const collapsed = rand() < 0.15;
      tree.mount(parent, { id, focusable: true }, {
        [id]: collapsed ? { width: 0, height: 0 } : { width: 80, height: 30 },
      });
      liveIds.push(id);
      focusableSet.add(id);
    };

    const opRemount = (): void => {
      if (graveyard.length === 0) return;
      const id = graveyard.splice(Math.floor(rand() * graveyard.length), 1)[0];
      if (tree.has(id)) return;
      const containers = ['app-root', 'main', 'footer', 'screen'].filter((c) => tree.has(c));
      if (containers.length === 0) return;
      tree.mount(pick(containers), { id, focusable: true }, { [id]: { width: 88, height: 32 } });
      liveIds.push(id);
    };

    const opFlush = (): void => {
      manager.flush();
    };

    const ops: Array<[number, () => void]> = [
      [3, opOpen],
      [3, opCloseRandom],
      [2, opDoubleClose],
      [2, opEscape],
      [2, opFinishExit],
      [3, opUnmount],
      [2, opMount],
      [2, opRemount],
      [2, opFlush],
    ];
    const totalWeight = ops.reduce((sum, [w]) => sum + w, 0);

    for (let step = 0; step < 240; step++) {
      let ticket = rand() * totalWeight;
      let chosen: () => void = opFlush;
      for (const [weight, fn] of ops) {
        ticket -= weight;
        if (ticket <= 0) {
          chosen = fn;
          break;
        }
      }
      chosen();
      if (rand() < 0.7) manager.flush();
      checkInvariants(`step ${step}`);
    }

    // 收尾：保证全局后备存在，逐层关闭并结束退出动画
    if (!tree.has('btn-fallback')) {
      const parent = tree.has('footer') ? 'footer' : tree.has('app-root') ? 'app-root' : 'screen';
      tree.mount(parent, { id: 'btn-fallback', focusable: true }, { 'btn-fallback': { width: 96, height: 32 } });
    }
    let guard = 0;
    while (manager.openLayerIds().length > 0) {
      assert.ok(guard++ < 500, `[seed ${seed}] 收尾关闭循环失控`);
      const ids = manager.openLayerIds();
      manager.closeLayer(ids[ids.length - 1]);
      manager.flush();
      checkInvariants('teardown-close');
    }
    for (const id of [...manager.exitingLayerIds()]) manager.finishExit(id);
    manager.flush();
    checkInvariants('final');

    assert.equal(manager.hasPendingFocus(), false, `[seed ${seed}] 待决焦点请求必须全部释放`);
    assert.equal(tree.totalInertClaims(), 0, `[seed ${seed}] inert 引用必须全部释放`);
  }
});
