import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ManualClock } from '../src/clock.ts';
import { SimulatedNetwork } from '../src/transport.ts';
import { SwimNode } from '../src/swim.ts';
import type { SwimEvent } from '../src/swim.ts';
import type { NodeId } from '../src/messages.ts';

const PERIOD = 1000;
const PING_TIMEOUT = 300;
const INDIRECT_TIMEOUT = 500;
const LATENCY = 10;
const IDS = ['A', 'B', 'C', 'D'];

type TimedEvent = SwimEvent & { at: number };

function makeCluster() {
  const clock = new ManualClock();
  const network = new SimulatedNetwork(clock, LATENCY);
  const events: TimedEvent[] = [];
  const nodes = new Map<NodeId, SwimNode>();
  for (const id of IDS) {
    // random() => 0：确定性选择——A 的首个探测目标是 B，代理依次是 C、D。
    const node = new SwimNode({
      id,
      peers: IDS,
      clock,
      transport: network.attach(id),
      periodMs: PERIOD,
      pingTimeoutMs: PING_TIMEOUT,
      indirectTimeoutMs: INDIRECT_TIMEOUT,
      k: 2,
      random: () => 0,
      onEvent: (event) => events.push({ ...event, at: clock.now() }),
    });
    nodes.set(id, node);
  }
  return { clock, network, events, nodes };
}

function findEvent(events: TimedEvent[], type: SwimEvent['type'], node: NodeId): TimedEvent | undefined {
  return events.find((e) => e.type === type && 'node' in e && e.node === node);
}

test('直连通畅时：Ping 直接收到 Ack，不触发 PingReq', () => {
  const { clock, network, events, nodes } = makeCluster();
  for (const node of nodes.values()) node.start();

  clock.advance(PERIOD); // t=1000：各节点发起周期探测
  clock.advance(100); // 消息往返（时延 10ms）

  assert.equal(nodes.get('A')!.statusOf('B'), 'alive');
  const success = findEvent(events, 'probe-success', 'A');
  assert.ok(success, 'A 应判定探测成功');
  assert.equal(network.count('ping-req'), 0, '直连成功时不应发送 PingReq');
});

test('验收场景：A 直连 B 丢包超时，C/D 代理探针在时限内带回 Ack，B 不被误判 Down', () => {
  const { clock, network, events, nodes } = makeCluster();
  // 弱网：A<->B 直连双向丢包，但 A<->C、A<->D、C<->B、D<->B 均通畅。
  network.dropBidirectional('A', 'B');
  for (const node of nodes.values()) node.start();

  clock.advance(PERIOD); // t=1000：A 向 B 发出直连 Ping（被丢弃）
  clock.advance(PING_TIMEOUT); // t=1300：直连超时，A 触发 K=2 个代理（C、D）PingReq
  clock.advance(INDIRECT_TIMEOUT); // t=1800：间接探测窗口结束

  // 1) PingReq 确实发给了 C 和 D
  const pingReq = findEvent(events, 'ping-req', 'A');
  assert.ok(pingReq && pingReq.type === 'ping-req');
  assert.deepEqual(pingReq.proxies, ['C', 'D']);
  assert.equal(network.count('ping-req', 'A', 'C'), 1);
  assert.equal(network.count('ping-req', 'A', 'D'), 1);

  // 2) A 在间接探测时限内收到了经代理转发的 Ack
  const ack = events.find((e) => e.type === 'ack' && e.node === 'A' && e.target === 'B');
  assert.ok(ack, 'A 应收到关于 B 的 Ack');
  assert.ok(ack.at < PERIOD + PING_TIMEOUT + INDIRECT_TIMEOUT, 'Ack 必须在间接超时前到达');
  assert.ok(ack.via === 'C' || ack.via === 'D', 'Ack 应经由代理 C 或 D 转发');

  // 3) 探测成功，B 未被错误标记为 Down
  const success = findEvent(events, 'probe-success', 'A');
  assert.ok(success, 'A 应判定对 B 的探测成功');
  assert.equal(nodes.get('A')!.statusOf('B'), 'alive');
  assert.equal(findEvent(events, 'mark-down', 'A'), undefined, 'A 不应标记任何节点 Down');
});

test('B 完全失联（所有路径均丢包）：直连与间接探测都超时，B 被标记 Down', () => {
  const { clock, network, events, nodes } = makeCluster();
  for (const id of ['A', 'C', 'D']) network.dropBidirectional(id, 'B');
  for (const node of nodes.values()) node.start();

  clock.advance(PERIOD + PING_TIMEOUT + INDIRECT_TIMEOUT + 100);

  assert.equal(nodes.get('A')!.statusOf('B'), 'down');
  const fail = findEvent(events, 'probe-fail', 'A');
  assert.ok(fail, 'A 应判定对 B 的探测失败');
  const markDown = findEvent(events, 'mark-down', 'A');
  assert.ok(markDown && markDown.type === 'mark-down');
  assert.equal(markDown.target, 'B');
});

test('被标记 Down 的节点不再作为探测目标或代理', () => {
  const { clock, network, events, nodes } = makeCluster();
  for (const id of ['A', 'C', 'D']) network.dropBidirectional(id, 'B');
  for (const node of nodes.values()) node.start();

  clock.advance(PERIOD + PING_TIMEOUT + INDIRECT_TIMEOUT + 100); // 第一周期：B 被判 Down
  clock.advance(PERIOD); // 第二周期开始

  const a = nodes.get('A')!;
  assert.deepEqual(a.aliveMembers(), ['C', 'D']);
  const secondPeriod = events.filter((e) => e.type === 'period-start' && e.node === 'A').at(-1);
  assert.ok(secondPeriod && secondPeriod.type === 'period-start');
  assert.notEqual(secondPeriod.target, 'B', 'Down 节点不应再被选为探测目标');
});
