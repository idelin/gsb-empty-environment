import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildMultiRingTopology } from '../src/topology.ts';
import { fullSpf } from '../src/spf.ts';
import type { Network } from '../src/network.ts';
import type { Topology } from '../src/types.ts';

/** 全网当前物理拓扑（仅 Up 链路） */
function physicalTopo(net: Network): Topology {
  const topo: Topology = new Map();
  for (const id of net.routers.keys()) topo.set(id, new Map());
  const seen = new Set<string>();
  for (const router of net.routers.values()) {
    for (const [nbr, iface] of router.interfaces) {
      if (!iface.up) continue;
      const key = [router.id, nbr].sort().join('|');
      if (seen.has(key)) continue;
      seen.add(key);
      topo.get(router.id)!.set(nbr, iface.cost);
      topo.get(nbr)!.set(router.id, iface.cost);
    }
  }
  return topo;
}

function assertNoLoopNoBlackhole(net: Network, ctx: string): void {
  const { loops, blackholes } = net.verifyForwarding();
  assert.deepEqual(loops, [], `${ctx}: 存在环路`);
  assert.deepEqual(blackholes, [], `${ctx}: 存在黑洞`);
}

function assertOptimal(net: Network, ctx: string): void {
  const topo = physicalTopo(net);
  for (const [id, router] of net.routers) {
    const { dist } = fullSpf(topo, id);
    for (const [dest, route] of router.routes) {
      assert.equal(route.cost, dist.get(dest), `${ctx}: ${id}->${dest} 代价非最优`);
    }
  }
}

test('8 节点多环拓扑：初始收敛，无环路无黑洞，路由最优', () => {
  const net = buildMultiRingTopology();
  const ticks = net.runUntilConverged();
  assert.ok(net.converged, '应收敛');
  assert.ok(ticks <= 3, `初始收敛应在 3 tick 内, 实际 ${ticks}`);
  assertNoLoopNoBlackhole(net, '初始收敛');
  assertOptimal(net, '初始收敛');
  // R1->R6 应走核心主干: R1-R2-R3-R5-R6 = 5
  const r1 = net.routers.get('R1')!;
  assert.equal(r1.routes.get('R6')!.cost, 5);
  assert.equal(r1.routes.get('R6')!.nextHop, 'R2');
});

test('切断核心主干 R3-R5：分布式 LSA 更新 + 增量 SPF，若干 tick 内收敛且无瞬时黑洞环路', () => {
  const net = buildMultiRingTopology();
  net.runUntilConverged();
  net.cutLink('R3', 'R5');
  let ticks = 0;
  while (!net.converged && ticks < 10) {
    net.tick();
    ticks += 1;
    // 每个 tick 边界都不得出现瞬时黑洞/环路
    assertNoLoopNoBlackhole(net, `断纤后 tick ${net.tickCount}`);
  }
  assert.ok(net.converged, '断纤后应收敛');
  assert.ok(ticks <= 3, `断纤后应在 3 tick 内收敛, 实际 ${ticks}`);
  assertOptimal(net, '断纤收敛后');
  // 绕行备用主干: R1-R4-R8-R5-R6 = 1+3+1+1 = 6
  const r1 = net.routers.get('R1')!;
  assert.equal(r1.routes.get('R6')!.cost, 6);
  assert.equal(r1.routes.get('R6')!.nextHop, 'R4');
  // 断纤为劣化事件，应使用增量 SPF
  assert.ok(
    net.logs.some((l) => l.includes('SPF 重算(mode=incremental')),
    '应存在增量 SPF 日志',
  );
  assert.ok(net.logs.some((l) => l.includes('发起 LSA')), '应有 LSA 发起日志');
});

test('权值变更触发重收敛，路由保持最优', () => {
  const net = buildMultiRingTopology();
  net.runUntilConverged();
  net.setCost('R4', 'R8', 1); // 备用主干变得更便宜
  const ticks = net.runUntilConverged();
  assert.ok(net.converged);
  assert.ok(ticks <= 3);
  assertNoLoopNoBlackhole(net, '权值变更后');
  assertOptimal(net, '权值变更后');
  // R1->R6: R1-R4-R8-R5-R6 = 1+1+1+1 = 4
  assert.equal(net.routers.get('R1')!.routes.get('R6')!.cost, 4);
});

test('断纤后恢复：改善型变化回退全量 SPF 并恢复最优路径', () => {
  const net = buildMultiRingTopology();
  net.runUntilConverged();
  net.cutLink('R3', 'R5');
  net.runUntilConverged();
  net.restoreLink('R3', 'R5');
  net.runUntilConverged();
  assertNoLoopNoBlackhole(net, '链路恢复后');
  assertOptimal(net, '链路恢复后');
  assert.equal(net.routers.get('R1')!.routes.get('R6')!.cost, 5);
  assert.ok(
    net.logs.some((l) => l.includes('SPF 重算(mode=full') && l.includes('up')),
    '恢复链路应触发全量 SPF',
  );
});

test('链路反复抖动：触发路由震荡抑制日志，衰减后自动解除', () => {
  const net = buildMultiRingTopology();
  net.runUntilConverged();
  // 快速抖动 R3-R5：down/up 各 2 次，惩罚值 2*1000 >= 抑制阈值 2000
  for (let i = 0; i < 2; i += 1) {
    net.cutLink('R3', 'R5');
    net.tick();
    net.restoreLink('R3', 'R5');
    net.tick();
  }
  const r3 = net.routers.get('R3')!;
  assert.ok(r3.isSuppressed('R5'), 'R3 应抑制 R3-R5 的 Up 通告');
  assert.ok(net.logs.some((l) => l.includes('震荡抑制')), '应输出震荡抑制日志');
  // 被抑制期间 R3 的 LSA 仍把 R3-R5 通告为 down
  const lsa = r3.lsdb.get('R3')!;
  assert.equal(lsa.links.find((l) => l.neighbor === 'R5')!.up, false);
  // 等待惩罚衰减，解除抑制后全网恢复最优
  net.runUntilConverged(15);
  assert.ok(!r3.isSuppressed('R5'), '衰减后应解除抑制');
  assert.ok(net.logs.some((l) => l.includes('解除抑制')), '应输出解除抑制日志');
  assertNoLoopNoBlackhole(net, '解除抑制后');
  assertOptimal(net, '解除抑制后');
});

test('分区场景：不可达目的地不产生路由（合法不可达，非黑洞）', () => {
  const net = buildMultiRingTopology();
  net.runUntilConverged();
  // 切断两条主干，环A 与 环B 分区
  net.cutLink('R3', 'R5');
  net.cutLink('R4', 'R8');
  net.runUntilConverged();
  const { loops, blackholes } = net.verifyForwarding();
  assert.deepEqual(loops, []);
  assert.deepEqual(blackholes, []); // 跨区目的地物理不可达，不计为黑洞
  assert.equal(net.routers.get('R1')!.routes.has('R6'), false);
});
