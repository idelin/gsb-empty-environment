import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BoundedClock } from '../src/clock.ts';
import { Simulator } from '../src/simulator.ts';
import type { SimEvent } from '../src/types.ts';
import {
  ACCEPTANCE_CONFIG,
  SCENARIO_END_TICK,
  buildAcceptanceScenario,
  runAcceptanceScenario,
} from '../src/scenario.ts';

function find(events: SimEvent[], type: string, node: string): SimEvent | undefined {
  return events.find((e) => e.type === type && e.node === node);
}

function noSplitBrainListener(violations: string[]) {
  return (sim: Simulator): void => {
    const writers = [...sim.nodes.values()].filter((n) => n.canServeWrites).map((n) => n.id);
    if (writers.length > 1) {
      violations.push(`tick ${sim.tick}: writable primaries = ${writers.join(',')}`);
    }
  };
}

test('验收: 主节点 tick 100 发起租约, 多数派 tick 105 确认', () => {
  const sim = runAcceptanceScenario();
  const start = find(sim.events, 'ELECTION_START', 'A');
  assert.equal(start?.tick, 100);
  const grant = find(sim.events, 'LEASE_GRANTED', 'A');
  assert.equal(grant?.tick, 105);
  assert.match(grant?.detail ?? '', /majority=\[A,B\]/);
  assert.match(grant?.detail ?? '', /demoteAtLocal=150/);
});

test('验收: 黑洞后旧主 tick 150 降级为候选者, 新主 tick 155 由多数派选出', () => {
  const sim = runAcceptanceScenario();
  const demote = find(sim.events, 'PRIMARY_DEMOTED', 'A');
  assert.equal(demote?.tick, 150);
  assert.match(demote?.detail ?? '', /lease_expired_without_majority_renewal/);
  const grant = find(sim.events, 'LEASE_GRANTED', 'B');
  assert.equal(grant?.tick, 155);
  assert.match(grant?.detail ?? '', /majority=\[B,C\]/);
});

test('验收: 新多数派拒绝旧主请求, 旧主最终转为跟随者', () => {
  const sim = runAcceptanceScenario();
  const rejects = sim.events.filter(
    (e) => e.type === 'LEASE_REJECT' && e.tick > 155 && e.detail.includes('to=A'),
  );
  assert.ok(rejects.length >= 2, 'B 和 C 都应拒绝旧主 A 的租约请求');
  assert.ok(rejects.every((r) => r.detail.includes('LEASE_HELD')));
  assert.equal(sim.node('B').role, 'PRIMARY');
  assert.equal(sim.node('A').role, 'FOLLOWER');
  assert.equal(sim.node('C').role, 'FOLLOWER');
});

test('验收: 杜绝脑裂写入 —— 任意 tick 至多一个可写主', () => {
  const sim = buildAcceptanceScenario();
  const violations: string[] = [];
  sim.addTickListener(noSplitBrainListener(violations));
  sim.runUntil(SCENARIO_END_TICK);
  assert.deepEqual(violations, []);

  const w1 = sim.events.find((e) => e.type === 'WRITE_COMMITTED' && e.detail.includes('w1'));
  assert.equal(w1?.node, 'A');
  assert.equal(w1?.tick, 149);
  const w2 = sim.events.find((e) => e.type === 'WRITE_REJECTED' && e.detail.includes('w2'));
  assert.equal(w2?.node, 'A');
  assert.equal(w2?.tick, 151);
  const w3 = sim.events.find((e) => e.type === 'WRITE_COMMITTED' && e.detail.includes('w3'));
  assert.equal(w3?.node, 'B');
  assert.equal(w3?.tick, 156);
  const w4 = sim.events.find((e) => e.type === 'WRITE_REJECTED' && e.detail.includes('w4'));
  assert.equal(w4?.node, 'A');
  assert.equal(w4?.tick, 170);
  assert.deepEqual(sim.node('A').writes.map((w) => w.value), ['w1']);
  assert.deepEqual(sim.node('B').writes.map((w) => w.value), ['w3']);
});

test('续期: 多数派心跳应答成功时租约持续续期, 主不降级', () => {
  const sim = new Simulator(ACCEPTANCE_CONFIG);
  sim.at(100, () => sim.startElection('A'));
  sim.runUntil(200);
  const renewals = sim.eventsOfType('LEASE_RENEWED').filter((e) => e.node === 'A');
  assert.ok(renewals.length >= 3, `期望多次续期, 实际 ${renewals.length}`);
  assert.equal(sim.eventsOfType('PRIMARY_DEMOTED').length, 0);
  assert.equal(sim.node('A').role, 'PRIMARY');
  assert.equal(sim.node('A').lease?.holder, 'A');
});

test('安全: 无多数派应答时不授予租约, 候选者按 term 重试', () => {
  const sim = new Simulator(ACCEPTANCE_CONFIG);
  sim.network.isolate('B');
  sim.network.isolate('C');
  sim.at(100, () => sim.startElection('A'));
  sim.runUntil(160);
  assert.equal(sim.eventsOfType('LEASE_GRANTED').length, 0);
  assert.equal(sim.node('A').role, 'CANDIDATE');
  assert.equal(sim.node('A').term, 1);
  const retries = sim.eventsOfType('ELECTION_RETRY').filter((e) => e.node === 'A');
  assert.ok(retries.length >= 3, `期望多次重试, 实际 ${retries.length}`);
});

test('时钟漂移受界: 偏移超过 maxSkew/2 被拒绝', () => {
  const clock = new BoundedClock('X', ACCEPTANCE_CONFIG.maxClockSkew);
  clock.setOffset(2);
  assert.equal(clock.localTime(100), 102);
  clock.setOffset(-2.5);
  assert.equal(clock.localTime(100), 97.5);
  assert.throws(() => clock.setOffset(3), /out of bound/);
  assert.throws(() => clock.setOffset(-3), /out of bound/);
});

test('时钟漂移受界: 快慢钟节点下旧主降级仍先于新租约生效, 无脑裂', () => {
  const sim = buildAcceptanceScenario();
  sim.node('A').clock.setOffset(-2); // A 时钟偏慢
  sim.node('B').clock.setOffset(2); // B 时钟偏快
  const violations: string[] = [];
  sim.addTickListener(noSplitBrainListener(violations));
  sim.runUntil(SCENARIO_END_TICK);

  const demote = find(sim.events, 'PRIMARY_DEMOTED', 'A');
  const grant = find(sim.events, 'LEASE_GRANTED', 'B');
  assert.equal(demote?.tick, 150);
  assert.equal(grant?.tick, 155);
  assert.ok(
    demote!.tick + ACCEPTANCE_CONFIG.maxClockSkew <= grant!.tick,
    '旧主保守降级点 + maxSkew 应不晚于新租约生效点',
  );
  assert.deepEqual(violations, []);
});
