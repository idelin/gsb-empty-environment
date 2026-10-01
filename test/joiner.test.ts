import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TemporalJoiner } from '../src/joiner.ts';
import type { JoinOutput, StreamEvent } from '../src/joiner.ts';

const L = (key: string, ts: number): StreamEvent => ({ kind: 'left', key, ts });
const R = (key: string, ts: number): StreamEvent => ({ kind: 'right', key, ts });
const C = (key: string, ts: number): StreamEvent => ({ kind: 'cancel', key, ts });

/** 依次喂入事件并收集全部输出 */
function drain(joiner: TemporalJoiner, events: StreamEvent[]): JoinOutput[] {
  return events.flatMap((e) => joiner.process(e));
}

test('验收场景一：L1@t=0 到达，R1@t=3 到达，立即输出配对结果', () => {
  const joiner = new TemporalJoiner({ windowMs: 5000 });

  assert.deepEqual(joiner.process(L('k1', 0)), []);

  const outputs = joiner.process(R('k1', 3));
  assert.equal(outputs.length, 1);
  assert.equal(outputs[0]!.type, 'matched');
  assert.equal(outputs[0]!.key, 'k1');
  if (outputs[0]!.type === 'matched') {
    assert.equal(outputs[0]!.left.ts, 0);
    assert.equal(outputs[0]!.right.ts, 3);
  }

  // 超时点之后不应再产生任何输出，状态已随配对释放
  assert.deepEqual(joiner.advanceWatermark(10_000), []);
  assert.equal(joiner.getStateSize(), 0);
});

test('验收场景二：L2@t=10 到达，t=12 取消，t=15 超时不触发孤立输出', () => {
  const joiner = new TemporalJoiner({ windowMs: 5000 });

  assert.deepEqual(joiner.process(L('k2', 10_000)), []);
  assert.deepEqual(joiner.process(C('k2', 12_000)), []);

  // 水位线越过原截止时间 t=15：不得输出 left-only
  const timeouts = joiner.advanceWatermark(15_000);
  assert.deepEqual(timeouts, []);
  assert.equal(joiner.getStateSize(), 0);
});

test('超时无匹配：输出 left-only 孤立结果', () => {
  const joiner = new TemporalJoiner({ windowMs: 5000 });

  joiner.process(L('k1', 0));
  const outputs = joiner.advanceWatermark(5_000);

  assert.equal(outputs.length, 1);
  assert.equal(outputs[0]!.type, 'left-only');
  assert.equal(outputs[0]!.key, 'k1');
});

test('迟到的 R 补配：L 已超时输出 left-only，窗口内的 R 晚到触发 late-matched 修正', () => {
  const joiner = new TemporalJoiner({ windowMs: 5000 });

  joiner.process(L('k1', 0));
  const timeouts = joiner.advanceWatermark(5_000);
  assert.equal(timeouts[0]!.type, 'left-only');

  // R 的事件时间 t=4 落在窗口 [0, 5] 内，只是到达晚了
  const late = joiner.process(R('k1', 4_000));
  assert.equal(late.length, 1);
  assert.equal(late[0]!.type, 'late-matched');
  assert.equal(late[0]!.key, 'k1');
});

test('乱序综合测试：R 先于 L 到达、取消先于 L 到达，最终状态零泄漏', () => {
  const joiner = new TemporalJoiner({ windowMs: 5000 });

  // 1) R 乱序先于 L 到达：L 到达时应立即配对
  assert.deepEqual(joiner.process(R('early', 1_000)), []);
  const matched = joiner.process(L('early', 0));
  assert.equal(matched.length, 1);
  assert.equal(matched[0]!.type, 'matched');

  // 2) 取消乱序先于 L 到达：L 到达后被墓碑吞掉，无任何输出
  assert.deepEqual(joiner.process(C('ghost', 2_000)), []);
  assert.deepEqual(joiner.process(L('ghost', 1_000)), []);

  // 3) 多 key 交错乱序
  const outputs = drain(joiner, [
    L('a', 10_000),
    R('b', 12_000), // b 的 R 先到
    L('b', 11_000), // → 立即 matched
    R('a', 14_500), // → 立即 matched
    L('c', 20_000), // c 永无匹配
  ]);
  assert.deepEqual(
    outputs.map((o) => `${o.type}:${o.key}`),
    ['matched:b', 'matched:a'],
  );

  // 4) 水位线扫过所有事件时间 + 保留期：c 超时输出 left-only，随后全部状态回收
  const final = joiner.advanceWatermark(100_000);
  assert.deepEqual(
    final.map((o) => `${o.type}:${o.key}`),
    ['left-only:c'],
  );
  assert.equal(joiner.getStateSize(), 0, '所有内部状态必须被回收，零泄漏');
});

test('窗口边界：R.ts 恰等于截止时间仍算窗口内，超过则不算', () => {
  const joiner = new TemporalJoiner({ windowMs: 5000 });

  joiner.process(L('edge', 0));
  const inside = joiner.process(R('edge', 5_000));
  assert.equal(inside[0]!.type, 'matched');

  joiner.process(L('outside', 10_000));
  assert.deepEqual(joiner.process(R('outside', 15_001)), []); // 超窗，缓存为乱序 R
  const timeouts = joiner.advanceWatermark(15_000);
  assert.equal(timeouts[0]!.type, 'left-only');
});
