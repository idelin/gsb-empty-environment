import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile, type ProtocolSpec } from '../src/model.ts';
import { checkCompat } from '../src/checker.ts';

function run(a: ProtocolSpec, b: ProtocolSpec, capacity = 2) {
  return checkCompat(compile(a), compile(b), { capacity });
}

const spec = (t: object[], finals = ['end']): ProtocolSpec => ({
  name: 'p', initial: 's0', finals, transitions: t as never,
});

test('兼容: 简单 ping-pong 正常终止', () => {
  const a = spec([
    { from: 's0', to: 's1', send: 'ping' },
    { from: 's1', to: 'end', recv: 'pong' },
  ]);
  const b = spec([
    { from: 's0', to: 's1', recv: 'ping' },
    { from: 's1', to: 'end', send: 'pong' },
  ]);
  const r = run(a, b);
  assert.equal(r.ok, true);
  assert.ok(r.stats.terminals >= 1);
  assert.ok(r.sampleSession && r.sampleSession.length === 4);
});

test('兼容: τ 内部动作与流水线（容量 2 两条在途消息）', () => {
  const a = spec([
    { from: 's0', to: 't0', tau: true },
    { from: 't0', to: 's1', send: 'x' },
    { from: 's1', to: 's2', send: 'x' },
    { from: 's2', to: 'end', recv: 'y' },
  ]);
  const b = spec([
    { from: 's0', to: 's1', recv: 'x' },
    { from: 's1', to: 's2', recv: 'x' },
    { from: 's2', to: 'end', send: 'y' },
  ]);
  const r = run(a, b, 2);
  assert.equal(r.ok, true);
});

test('死锁: 双方都在等待，轨迹长度为 0', () => {
  const a = spec([{ from: 's0', to: 'end', recv: 'x' }]);
  const b = spec([{ from: 's0', to: 'end', recv: 'y' }]);
  const r = run(a, b);
  assert.equal(r.ok, false);
  assert.equal(r.issue!.type, 'deadlock');
  if (r.issue!.type === 'deadlock') assert.equal(r.issue!.kind, 'both-waiting');
  assert.equal(r.trace.length, 0);
});

test('死锁: 握手后双方互相等待（区分于正常终止）', () => {
  const a = spec([
    { from: 's0', to: 's1', send: 'hello' },
    { from: 's1', to: 's2', recv: 'ack' },
    { from: 's2', to: 'end', recv: 'ready' },
  ]);
  const b = spec([
    { from: 's0', to: 's1', recv: 'hello' },
    { from: 's1', to: 's2', send: 'ack' },
    { from: 's2', to: 'end', recv: 'go' },
  ]);
  const r = run(a, b);
  assert.equal(r.ok, false);
  assert.equal(r.issue!.type, 'deadlock');
  if (r.issue!.type === 'deadlock') assert.equal(r.issue!.kind, 'both-waiting');
  assert.equal(r.trace.length, 4); // hello, ?hello, ack, ?ack
  assert.ok(r.stats.terminals === 0);
});

test('意外接收: 对端收到无法处理的消息，轨迹最短', () => {
  const a = spec([
    { from: 's0', to: 's1', send: 'ping' },
    { from: 's1', to: 'end', send: 'quit' },
  ]);
  const b = spec([
    { from: 's0', to: 's1', recv: 'hello' },
    { from: 's1', to: 'end', recv: 'quit' },
  ]);
  const r = run(a, b);
  assert.equal(r.ok, false);
  assert.equal(r.issue!.type, 'unspecified-reception');
  if (r.issue!.type === 'unspecified-reception') {
    assert.equal(r.issue!.side, 'B');
    assert.equal(r.issue!.msg, 'ping');
    assert.deepEqual(r.issue!.expected, ['hello']);
  }
  assert.equal(r.trace.length, 1); // 只需 A!ping 一步
});

test('意外接收: τ 闭包内可接收则不误报', () => {
  // B 收到 x 时在 s0 不直接接收，但 τ 迁移后的状态可以接收
  const a = spec([
    { from: 's0', to: 'end', send: 'x' },
  ]);
  const b = spec([
    { from: 's0', to: 's1', tau: true },
    { from: 's1', to: 'end', recv: 'x' },
  ]);
  const r = run(a, b);
  assert.equal(r.ok, true);
});

test('活锁: 会话无限循环无法到达终止', () => {
  const a = spec([
    { from: 's0', to: 's1', send: 'ping' },
    { from: 's1', to: 's0', recv: 'pong' },
  ], ['end']);
  const b = spec([
    { from: 's0', to: 's1', recv: 'ping' },
    { from: 's1', to: 's0', send: 'pong' },
  ], ['end']);
  const r = run(a, b);
  assert.equal(r.ok, false);
  assert.equal(r.issue!.type, 'livelock');
  assert.equal(r.trace.length, 0); // 初始状态就无法到达终止
});

test('队列容量约束生效: 容量越小可到达状态空间越小', () => {
  const a = spec([
    { from: 's0', to: 's1', send: 'x' },
    { from: 's1', to: 's2', send: 'x' },
    { from: 's2', to: 'end', recv: 'y' },
  ]);
  const b = spec([
    { from: 's0', to: 's1', recv: 'x' },
    { from: 's1', to: 's2', recv: 'x' },
    { from: 's2', to: 'end', send: 'y' },
  ]);
  const r2 = run(a, b, 2);
  const r1 = run(a, b, 1);
  assert.equal(r2.ok, true);
  assert.equal(r1.ok, true);
  assert.ok(r1.stats.states < r2.stats.states);
});

test('正常终止要求队列清空: 终止时队列残留消息算意外接收', () => {
  const a = spec([
    { from: 's0', to: 'end', send: 'x' },
  ]);
  const b = spec([], ['s0']); // B 初始即终止，但 A 发来的 x 无人接收
  const r = run(a, b);
  assert.equal(r.ok, false);
  assert.equal(r.issue!.type, 'unspecified-reception');
});
