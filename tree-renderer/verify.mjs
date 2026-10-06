// verify.mjs — 用显式步进器重放交错的更新序列，核对可见版本与资源计数。
import assert from 'node:assert/strict';
import { VirtualHost, Renderer, Stepper, StaleCommitError } from './renderer.mjs';

const shape = (nodes) =>
  nodes.map((n) => ({ type: n.type, key: n.key, children: shape(n.children) }));

const countLiveInVisible = (renderer) => {
  let live = 0;
  const walk = (nodes) => {
    for (const n of nodes) {
      assert.equal(n.disposed, false, 'visible node must not be disposed');
      for (const s of n.subs) {
        assert.equal(s.disposed, false, `visible sub "${s.label}" must be live`);
        live++;
      }
      walk(n.children);
    }
  };
  walk(renderer.visibleTree);
  return live;
};

// 全局不变量：宿主账本里的存活订阅 == 可见树上实际挂着的订阅
const checkInvariant = (host, renderer) => {
  assert.equal(host.liveSubs(), countLiveInVisible(renderer), 'live subs mismatch');
  assert.equal(host.stats.doubleDispose, 0, 'double dispose detected');
};

let passed = 0;
const scenario = (name, fn) => {
  fn();
  passed++;
  console.log(`ok ${passed} - ${name}`);
};

// ---------------------------------------------------------------- 1. 基本提交
scenario('basic commit publishes exactly the prepared version', () => {
  const host = new VirtualHost();
  const stepper = new Stepper();
  const r = new Renderer(host, stepper);

  r.schedule(1, [
    { type: 'row', key: 'a', subs: ['tick'] },
    { type: 'row', key: 'b', subs: ['tick', 'geo'], children: [{ type: 'leaf', key: 'b1', subs: ['hover'] }] },
  ]);
  assert.equal(host.liveSubs(), 0, 'prepare must not create subs before units run');
  stepper.runUntilIdle();
  assert.equal(host.liveSubs(), 4, 'prepared subs live but not yet visible');
  assert.deepEqual(shape(r.visibleTree), [], 'visible tree untouched before commit');

  r.commit(1);
  assert.equal(r.version, 1);
  assert.equal(host.liveSubs(), 4);
  assert.equal(countLiveInVisible(r), 4);
  checkInvariant(host, r);
});

// ------------------------------------------------- 2. 准备中途被更新版本取代
scenario('superseded mid-prepare cleans its partial subscriptions', () => {
  const host = new VirtualHost();
  const stepper = new Stepper();
  const r = new Renderer(host, stepper);

  r.schedule(1, [
    { type: 'row', key: 'a', subs: ['s1'] },
    { type: 'row', key: 'b', subs: ['s2'] },
    { type: 'row', key: 'c', subs: ['s3'] },
  ]);
  stepper.step(2); // 只准备了 a、b 两个节点
  assert.equal(host.liveSubs(), 2);

  r.schedule(2, [{ type: 'row', key: 'x', subs: ['sx'] }]); // 取代 v1
  assert.equal(host.liveSubs(), 0, 'stale prepare subs must be disposed immediately');
  assert.equal(host.stats.disposedSubs, 2);

  stepper.runUntilIdle(); // v1 剩余单元变空操作，v2 准备完成
  r.commit(2);
  assert.equal(r.version, 2);
  assert.deepEqual(shape(r.visibleTree), [{ type: 'row', key: 'x', children: [] }]);
  assert.equal(host.liveSubs(), 1);
  checkInvariant(host, r);
});

// ------------------------------------------------------- 3. 过期提交被拒绝
scenario('stale or premature commits are rejected', () => {
  const host = new VirtualHost();
  const stepper = new Stepper();
  const r = new Renderer(host, stepper);

  r.schedule(1, [{ type: 'row', key: 'a', subs: ['s1'] }]);
  stepper.step(1); // 未准备完整
  assert.throws(() => r.commit(1), StaleCommitError, 'premature commit must throw');

  stepper.runUntilIdle(); // v1 完整
  r.schedule(2, [{ type: 'row', key: 'b', subs: ['s2'] }]); // v1 变陈旧
  assert.equal(host.liveSubs(), 0, 'completed-but-stale prepare subs cleaned');
  assert.throws(() => r.commit(1), StaleCommitError, 'stale commit must throw');

  stepper.runUntilIdle();
  r.commit(2);
  assert.equal(r.version, 2);
  checkInvariant(host, r);
});

// --------------------------------------- 4. 跨版本复用：旧订阅替换、移除节点清理
scenario('reconcile reuses nodes, replaces subs, disposes removed nodes once', () => {
  const host = new VirtualHost();
  const stepper = new Stepper();
  const r = new Renderer(host, stepper);

  r.schedule(1, [
    { type: 'row', key: 'keep', subs: ['old'] },
    { type: 'row', key: 'drop', subs: ['gone'] },
  ]);
  stepper.runUntilIdle();
  r.commit(1);
  const keepNode = r.visibleTree[0];
  const dropNode = r.visibleTree[1];

  r.schedule(2, [
    { type: 'row', key: 'keep', subs: ['new'] },
    { type: 'row', key: 'add', subs: ['fresh'] },
  ]);
  stepper.runUntilIdle();
  r.commit(2);

  assert.equal(r.visibleTree[0], keepNode, 'matched node identity preserved');
  assert.equal(keepNode.subs[0].label, 'new');
  assert.equal(dropNode.disposed, true);
  assert.equal(dropNode.disposeCount, 1, 'removed node disposed exactly once');
  assert.equal(host.liveSubs(), 2);
  assert.equal(host.stats.nodesDisposed, 1);
  checkInvariant(host, r);
});

// --------------------------------------------- 5. 暂停的准备不得触碰可见树
scenario('paused prepare never mutates the visible tree', () => {
  const host = new VirtualHost();
  const stepper = new Stepper();
  const r = new Renderer(host, stepper);

  r.schedule(1, [{ type: 'row', key: 'a', props: { n: 1 }, subs: ['s'] }]);
  stepper.runUntilIdle();
  r.commit(1);
  const before = JSON.stringify(shape(r.visibleTree));
  const propsBefore = JSON.stringify(r.visibleTree[0].props);

  r.schedule(2, [{ type: 'row', key: 'a', props: { n: 2 }, subs: ['s', 's2'] }]);
  stepper.step(1); // 准备进行到一半，暂停
  assert.equal(JSON.stringify(shape(r.visibleTree)), before);
  assert.equal(JSON.stringify(r.visibleTree[0].props), propsBefore);
  assert.equal(r.version, 1, 'visible version stays at last commit');

  stepper.runUntilIdle();
  r.commit(2);
  assert.equal(r.visibleTree[0].props.n, 2);
  assert.equal(r.version, 2);
  checkInvariant(host, r);
});

// ------------------------------------------------------- 6. 卸载清理恰一次
scenario('unmount cleans everything exactly once and is idempotent', () => {
  const host = new VirtualHost();
  const stepper = new Stepper();
  const r = new Renderer(host, stepper);

  r.schedule(1, [
    { type: 'row', key: 'a', subs: ['s1'], children: [{ type: 'leaf', key: 'a1', subs: ['s2'] }] },
    { type: 'row', key: 'b', subs: ['s3'] },
  ]);
  stepper.runUntilIdle();
  r.commit(1);
  const nodes = [];
  const collect = (ns) => ns.forEach((n) => { nodes.push(n); collect(n.children); });
  collect(r.visibleTree);

  r.unmount();
  assert.equal(host.liveSubs(), 0);
  assert.equal(nodes.every((n) => n.disposed && n.disposeCount === 1), true);
  const disposedAfterFirst = host.stats.disposedSubs;

  r.unmount(); // 幂等
  r.unmount();
  assert.equal(host.stats.disposedSubs, disposedAfterFirst, 'second unmount must be a no-op');
  assert.equal(nodes.every((n) => n.disposeCount === 1), true);
  assert.throws(() => r.schedule(2, []), /unmounted/);
  assert.throws(() => r.commit(2), /unmounted/);
});

// ------------------------------------------------- 7. 卸载时在途准备也被清理
scenario('unmount with an in-flight prepare disposes its resources', () => {
  const host = new VirtualHost();
  const stepper = new Stepper();
  const r = new Renderer(host, stepper);

  r.schedule(1, [{ type: 'row', key: 'a', subs: ['committed'] }]);
  stepper.runUntilIdle();
  r.commit(1);

  r.schedule(2, [
    { type: 'row', key: 'a', subs: ['wip1'] },
    { type: 'row', key: 'b', subs: ['wip2'] },
  ]);
  stepper.step(1); // 在途准备已创建 1 个订阅
  assert.equal(host.liveSubs(), 2);

  r.unmount();
  assert.equal(host.liveSubs(), 0, 'committed + in-flight subs all cleaned');
  stepper.runUntilIdle(); // 陈旧单元空操作，不得复活任何资源
  assert.equal(host.liveSubs(), 0);
  assert.equal(host.stats.doubleDispose, 0);
});

// ------------------------------------- 8. 脚本化重放：交错更新 + 最终账本核对
scenario('scripted replay of interleaved updates ends in a consistent ledger', () => {
  const host = new VirtualHost();
  const stepper = new Stepper();
  const r = new Renderer(host, stepper);

  const tree = (n) => [
    { type: 'row', key: 'a', subs: [`a@${n}`], children: [{ type: 'leaf', key: 'a1', subs: [`a1@${n}`] }] },
    { type: 'row', key: n % 2 ? 'odd' : 'even', subs: [`t@${n}`] },
  ];

  const ops = [
    ['schedule', 1, tree(1)],
    ['step', 2],
    ['assertLive', 2],            // v1 部分准备
    ['assertVisibleVersion', 0],
    ['schedule', 2, tree(2)],     // 取代 v1
    ['assertLive', 0],            // v1 部分订阅已清理
    ['step', 1],
    ['schedule', 3, tree(3)],     // 取代 v2
    ['assertLive', 0],
    ['run'],
    ['commit', 2, 'throw'],       // 过期提交
    ['commit', 3],
    ['assertVisibleVersion', 3],
    ['assertLive', 3],
    ['schedule', 4, tree(4)],
    ['step', 1],
    ['commit', 4, 'throw'],       // 未准备完整
    ['run'],
    ['commit', 4],
    ['assertVisibleVersion', 4],
    ['assertLive', 3],
    ['assertShape', [
      { type: 'row', key: 'a', children: [{ type: 'leaf', key: 'a1', children: [] }] },
      { type: 'row', key: 'even', children: [] },
    ]],
    ['unmount'],
    ['assertLive', 0],
    ['unmount'],
    ['assertLive', 0],
  ];

  for (const [op, ...args] of ops) {
    switch (op) {
      case 'schedule': r.schedule(args[0], args[1]); break;
      case 'step': stepper.step(args[0]); break;
      case 'run': stepper.runUntilIdle(); break;
      case 'commit':
        if (args[1] === 'throw') assert.throws(() => r.commit(args[0]), StaleCommitError);
        else r.commit(args[0]);
        break;
      case 'unmount': r.unmount(); break;
      case 'assertLive': assert.equal(host.liveSubs(), args[0], `live subs after ${op}`); break;
      case 'assertVisibleVersion': assert.equal(r.version, args[0]); break;
      case 'assertShape': assert.deepEqual(shape(r.visibleTree), args[0]); break;
      default: throw new Error(`unknown op ${op}`);
    }
    assert.equal(host.stats.doubleDispose, 0, 'no double dispose during replay');
  }

  // 最终账本：创建数 == 清理数（存活为 0），节点也全部回收
  assert.equal(host.stats.createdSubs, host.stats.disposedSubs);
  assert.equal(host.stats.nodesCreated, host.stats.nodesDisposed);
  console.log(`   replay ledger: subs ${host.stats.createdSubs} created / ${host.stats.disposedSubs} disposed, ` +
    `nodes ${host.stats.nodesCreated} created / ${host.stats.nodesDisposed} disposed`);
});

console.log(`\n${passed} scenarios passed`);
