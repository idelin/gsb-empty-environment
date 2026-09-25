import { test } from "node:test";
import assert from "node:assert/strict";
import { NodeTree } from "../src/tree.ts";
import { Scheduler } from "../src/scheduler.ts";
import { LayerManager } from "../src/layers.ts";
import {
  pageSpec,
  settingsLayerSpec,
  confirmLayerSpec,
  helpLayerSpec,
} from "../src/fixtures.ts";

/**
 * 状态探索:对操作序列做广度优先枚举,在每个可达状态上检查:
 *  I1 任一时刻只有顶层拥有焦点(焦点必在当前可交互范围内且非 inert);
 *  I2 inert 状态与可交互范围严格互补(含 closing 层的自我隔离);
 *  I3 重复关闭/收尾幂等(对未知 id 的操作不改变状态);
 *  I4 静止状态(无弹层、无动画、队列空)下所有 inert 引用已释放。
 */

interface World {
  tree: NodeTree;
  scheduler: Scheduler;
  mgr: LayerManager;
}

function buildWorld(): World {
  const tree = new NodeTree(pageSpec());
  const scheduler = new Scheduler();
  const mgr = new LayerManager(tree, scheduler);
  return { tree, scheduler, mgr };
}

const OPS: Array<(w: World) => void> = [
  (w) => {
    if (!w.tree.has("layer-settings")) {
      w.mgr.open({ id: "settings", content: settingsLayerSpec(), triggerId: "btn-settings" });
    }
  },
  (w) => {
    if (!w.tree.has("layer-confirm")) {
      w.mgr.open({ id: "confirm", content: confirmLayerSpec(), triggerId: "btn-open-confirm", exitAnimation: true });
    }
  },
  (w) => {
    if (!w.tree.has("layer-help")) {
      w.mgr.open({ id: "help", content: helpLayerSpec(), triggerId: "btn-help", dismissible: false });
    }
  },
  (w) => {
    const top = w.mgr.topLayerId();
    if (top !== null) w.mgr.close(top);
  },
  (w) => {
    w.mgr.escape();
  },
  (w) => {
    w.mgr.finalize("confirm");
  },
  (w) => {
    w.tree.remove("btn-settings");
  },
  (w) => {
    if (!w.tree.has("btn-settings")) {
      w.tree.mount("page", { id: "btn-settings", text: "Open Settings", focusable: true });
    }
  },
  (w) => {
    w.scheduler.tick(); // 单个异步步进:制造交错
  },
  (w) => {
    w.scheduler.flush();
  },
];

function fingerprint(w: World): string {
  return JSON.stringify({
    state: w.mgr.debugState(),
    nodes: w.tree.documentOrder(),
    pending: w.scheduler.pending,
  });
}

function replay(path: number[]): World {
  const world = buildWorld();
  for (const op of path) OPS[op](world);
  return world;
}

function checkInvariants(w: World, path: number[]): void {
  const { tree, mgr } = w;
  const scope = mgr.scopeRoot();
  const closingRoots = mgr.closingRoots();

  // I1: 焦点(若存在)必在顶层可交互范围内、已挂载、可聚焦、非 inert
  const focus = mgr.focus.active;
  if (focus !== null) {
    assert.ok(tree.has(focus), `焦点 ${focus} 必须已挂载`);
    assert.ok(tree.contains(scope, focus), `焦点 ${focus} 必须在顶层范围内`);
    assert.ok(!mgr.inert.isInert(focus, tree), `焦点 ${focus} 不得 inert`);
    assert.ok(mgr.focus.isFocusable(focus), `焦点 ${focus} 必须可聚焦`);
  }

  // I2: inert ⟺ 不在可交互范围(closing 层自我隔离也算范围外)
  for (const id of tree.documentOrder()) {
    // 范围根的祖先容器(如文档根)不参与 inert 判定
    if (id !== scope && tree.contains(id, scope)) continue;
    const inScope =
      tree.contains(scope, id) && !closingRoots.some((root) => tree.contains(root, id));
    assert.equal(
      mgr.inert.isInert(id, tree),
      !inScope,
      `节点 ${id} 的 inert 状态与可交互范围不互补 (路径 ${JSON.stringify(path)}, 状态 ${JSON.stringify(mgr.debugState())})`,
    );
  }

  // I3: 对未知/已关闭 id 的重复关闭与收尾是幂等 no-op
  const before = fingerprint(w);
  assert.equal(mgr.close("never-opened"), false);
  assert.equal(mgr.finalize("never-opened"), false);
  assert.equal(fingerprint(w), before, "幂等操作不得改变状态");

  // I4: 静止状态下所有引用最终释放
  if (mgr.topLayerId() === null && closingRoots.length === 0 && w.scheduler.pending === 0) {
    assert.equal(mgr.inert.totalRefs, 0, "全部关闭后不得残留 inert 引用");
    if (focus !== null) {
      assert.ok(tree.contains("page", focus), `静止时焦点 ${focus} 必须回到页面`);
    }
  }
}

test("状态探索:嵌套/交错操作下焦点、inert 与幂等不变量", () => {
  const MAX_DEPTH = 6;
  const seen = new Set<string>();
  const queue: number[][] = [[]];
  seen.add(fingerprint(buildWorld()));
  let visited = 0;
  let quiescent = 0;
  let head = 0;

  while (head < queue.length) {
    const path = queue[head++];
    const world = replay(path);
    visited++;
    checkInvariants(world, path);
    if (
      world.mgr.topLayerId() === null &&
      world.mgr.closingRoots().length === 0 &&
      world.scheduler.pending === 0
    ) {
      quiescent++;
    }
    if (path.length >= MAX_DEPTH) continue;
    for (let op = 0; op < OPS.length; op++) {
      const next = [...path, op];
      const fp = fingerprint(replay(next));
      if (!seen.has(fp)) {
        seen.add(fp);
        queue.push(next);
      }
    }
  }

  assert.ok(visited > 500, `探索应覆盖足够状态,实际 ${visited}`);
  assert.ok(quiescent > 0, "探索必须到达静止状态");
  console.log(`  探索状态数: ${visited},其中静止状态: ${quiescent}`);
});
