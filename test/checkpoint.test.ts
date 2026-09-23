// 检查点：绑定前缀摘要、增量恢复、篡改拒绝、日志压缩与回收。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Interpreter } from '../src/interpreter.ts';
import {
  createCheckpoint,
  verifyCheckpoint,
  recover,
  compact,
  reclaimCheckpoints,
} from '../src/checkpoint.ts';
import { CheckpointError } from '../src/types.ts';
import { fullLogInterpret } from '../src/differential.ts';
import { deepClone } from '../src/hash.ts';
import { buildSampleLog, randomLog, mulberry32 } from './helpers.ts';

function checkpointAt(records: Parameters<typeof fullLogInterpret>[0], uptoSeq: number, id: string) {
  const itp = new Interpreter(records.slice(0, uptoSeq));
  itp.runToEnd();
  return createCheckpoint(id, itp);
}

// 找出所有无打开事务的位置（合法的检查点切点）。
function committedBoundaries(records: Parameters<typeof fullLogInterpret>[0]): number[] {
  const itp = new Interpreter(records);
  const out: number[] = [];
  for (let i = 0; i < records.length; i++) {
    itp.step();
    if (!itp.hasOpenTx()) out.push(i + 1);
  }
  return out;
}

test('检查点增量恢复 = 全量恢复 = 独立解释器基准', () => {
  const { records, truth } = buildSampleLog();
  const firedFull: string[] = [];
  const full = recover(records, [], (e) => firedFull.push(e));
  assert.equal(full.usedCheckpoint, null);
  assert.equal(full.interpreter.summary(), truth.summary);

  const cp = checkpointAt(records, 6, 'cp@6');
  const firedInc: string[] = [];
  const inc = recover(records, [cp], (e) => firedInc.push(e));
  assert.equal(inc.usedCheckpoint?.id, 'cp@6');
  assert.equal(inc.interpreter.summary(), truth.summary, '增量恢复摘要必须等于基准');
  assert.deepEqual(firedInc, firedFull, '两种方式触发的副作用必须一致');
});

test('检查点绑定前缀摘要：摘要被篡改则拒绝使用', () => {
  const { records, truth } = buildSampleLog();
  const cp = checkpointAt(records, 6, 'cp@6');
  const tampered = deepClone(cp);
  tampered.state['balance:ada'] = -999; // 篡改快照内容
  assert.equal(verifyCheckpoint(tampered), false);
  const { usedCheckpoint, interpreter } = recover(records, [tampered]);
  assert.equal(usedCheckpoint, null, '篡改的检查点必须被弃用');
  assert.equal(interpreter.summary(), truth.summary, '回退全量恢复仍须正确');
});

test('前缀摘要不符的检查点不得作为恢复基点', () => {
  const { records, truth } = buildSampleLog();
  const cp = checkpointAt(records, 6, 'cp@6');
  const foreign = deepClone(cp);
  foreign.prefixHash = 'ff'.repeat(32); // 与日志前缀不符
  foreign.hash = cp.hash; // 即使自带摘要也匹配不上日志
  const { usedCheckpoint, interpreter } = recover(records, [foreign]);
  assert.equal(usedCheckpoint, null);
  assert.equal(interpreter.summary(), truth.summary);
});

test('日志压缩：回收检查点之前的历史，剩余链校验连续且恢复一致', () => {
  const { records, truth } = buildSampleLog();
  const cp = checkpointAt(records, 6, 'cp@6');
  const { segment, tail } = compact(records, cp);
  assert.equal(segment.anchor.body.kind, 'branch');
  assert.equal(tail[0].prev, cp.prefixHash, '压缩后首条必须链接到检查点摘要');
  assert.equal(tail[0].seq, 7);
  // 用压缩段（检查点 + 尾部）恢复
  const fired: string[] = [];
  const { interpreter } = recover(tail, [segment.checkpoint], (e) => fired.push(e));
  assert.equal(interpreter.summary(), truth.summary);
  assert.deepEqual(fired, ['eff-points-1']);
});

test('检查点与日志前缀不符时拒绝压缩回收', () => {
  const { records } = buildSampleLog();
  const cp = checkpointAt(records, 6, 'cp@6');
  const other = randomLog(mulberry32(9), 20); // 另一条不相干的日志
  assert.throws(() => compact(other, cp), CheckpointError);
});

test('检查点回收：只保留最新，旧检查点可安全丢弃', () => {
  const { records, truth } = buildSampleLog();
  const cps = [checkpointAt(records, 2, 'cp@2'), checkpointAt(records, 6, 'cp@6'), checkpointAt(records, 8, 'cp@8')];
  const kept = reclaimCheckpoints(cps, 1);
  assert.deepEqual(kept.map((c) => c.id), ['cp@8']);
  const { interpreter, usedCheckpoint } = recover(records, kept);
  assert.equal(usedCheckpoint?.id, 'cp@8');
  assert.equal(interpreter.summary(), truth.summary);
});

test('存在未提交事务时禁止创建检查点', () => {
  const { records } = buildSampleLog();
  const beginIdx = records.findIndex((r) => r.body.kind === 'tx.begin');
  const itp = new Interpreter(records.slice(0, beginIdx + 1));
  itp.runToEnd();
  assert.throws(() => createCheckpoint('bad', itp), CheckpointError);
});

test('随机日志多检查点恢复对照（含回收后恢复）', () => {
  const rng = mulberry32(2024);
  for (let round = 0; round < 20; round++) {
    const records = randomLog(rng, 40);
    const truth = fullLogInterpret(records);
    const bounds = committedBoundaries(records);
    const picks = [bounds[2], bounds[Math.floor(bounds.length / 2)], bounds[bounds.length - 3]].filter(
      (n): n is number => typeof n === 'number',
    );
    const cps = [...new Set(picks)].map((n) => checkpointAt(records, n, `cp@${n}`));
    const kept = reclaimCheckpoints(cps, 1);
    const { interpreter } = recover(records, kept);
    assert.equal(interpreter.summary(), truth.summary, `第 ${round} 轮恢复摘要必须等于基准`);
  }
});
