import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DeterministicKernel } from '../src/kernel.ts';
import { Rng } from '../src/rng.ts';
import { canonicalize, hashState } from '../src/canonical.ts';
import type { TraceLog } from '../src/types.ts';

const kernel = new DeterministicKernel();
const RECORD_OPTS = { seed: 6, arrivalCount: 50, lossProb: 0.06, timeoutTicks: 8 };

function freshTrace() {
  return kernel.record(RECORD_OPTS);
}

test('录制轨迹包含 50 个网络包到达与 3 次超时', () => {
  const { log } = freshTrace();
  const arrivals = log.entries.filter((e) => e.kind === 'packet-arrival');
  const timeouts = log.entries.filter((e) => e.kind === 'timeout');
  assert.equal(arrivals.length, 50);
  assert.equal(timeouts.length, 3);
  assert.equal(log.entries.length, 53);
  // 事件序号连续且从 1 开始，tick 单调不减
  log.entries.forEach((e, i) => {
    assert.equal(e.index, i + 1);
    if (i > 0) assert.ok(e.tick >= log.entries[i - 1].tick);
  });
});

test('验收：重放到第 35 个事件，状态与历史快照哈希逐字节一致', () => {
  const { log, snapshots } = freshTrace();
  const { snapshot } = kernel.travelTo(log, 35);
  const historical = snapshots[35];
  assert.equal(snapshot.hash, historical.hash, 'SHA-256 哈希一致');
  assert.equal(snapshot.canonical, historical.canonical, '规范化字节串逐字节一致');
  assert.equal(snapshot.afterEvent, 35);
});

test('任意历史事件均可跳转并完全复现（0..53 全部快照）', () => {
  const { log, snapshots } = freshTrace();
  for (let i = 0; i <= log.entries.length; i++) {
    const { snapshot } = kernel.travelTo(log, i);
    assert.equal(snapshot.hash, snapshots[i].hash, `事件 #${i} 哈希不一致`);
    assert.equal(snapshot.canonical, snapshots[i].canonical, `事件 #${i} 字节不一致`);
  }
});

test('时间旅行无副作用：跳转不污染后续重放结果', () => {
  const { log, snapshots } = freshTrace();
  kernel.travelTo(log, 35);
  kernel.travelTo(log, 10);
  kernel.travelTo(log, 53);
  const again = kernel.travelTo(log, 35);
  assert.equal(again.snapshot.hash, snapshots[35].hash);
});

test('两次录制产生完全相同的日志与快照（确定性）', () => {
  const a = freshTrace();
  const b = freshTrace();
  assert.equal(canonicalize(a.log), canonicalize(b.log));
  assert.deepEqual(
    a.snapshots.map((s) => s.hash),
    b.snapshots.map((s) => s.hash),
  );
});

test('篡改时钟中断日志会被重放校验发现', () => {
  const { log } = freshTrace();
  const tampered: TraceLog = JSON.parse(JSON.stringify(log));
  const timeoutIdx = tampered.entries.findIndex((e) => e.kind === 'timeout');
  tampered.entries[timeoutIdx].tick += 1; // 超时由组件确定性重导，与日志矛盾
  assert.throws(() => kernel.travelTo(tampered, tampered.entries.length), /日志校验失败/);
});

test('篡改外部输入会导致状态哈希偏离历史快照', () => {
  const { log, snapshots } = freshTrace();
  const tampered: TraceLog = JSON.parse(JSON.stringify(log));
  tampered.entries[34].payload.size += 1; // 篡改第 35 个事件的包大小
  const { snapshot } = kernel.travelTo(tampered, 35);
  assert.notEqual(snapshot.hash, snapshots[35].hash);
});

test('快照哈希对状态差异敏感', () => {
  const { log, snapshots } = freshTrace();
  const { snapshot } = kernel.travelTo(log, 35);
  assert.notEqual(snapshot.hash, snapshots[34].hash);
  assert.notEqual(snapshot.hash, snapshots[36].hash);
});

test('Rng 状态可序列化并恢复', () => {
  const rng = new Rng(123);
  rng.next();
  rng.next();
  const saved = rng.getState();
  const a = rng.next();
  rng.setState(saved);
  assert.equal(rng.next(), a);
});

test('canonicalize 键序无关，hashState 稳定', () => {
  const x = { b: 1, a: { d: [3, 2], c: 'x' } };
  const y = { a: { c: 'x', d: [3, 2] }, b: 1 };
  assert.equal(canonicalize(x), canonicalize(y));
  assert.equal(hashState(x), hashState(y));
});
