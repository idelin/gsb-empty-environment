// 分支与拼接：前缀摘要不符拒绝拼接；拼接结果与独立解释器对照。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { forkLog, spliceBranch, verifyChain } from '../src/log.ts';
import { Interpreter } from '../src/interpreter.ts';
import { fullLogInterpret } from '../src/differential.ts';
import { SpliceError } from '../src/types.ts';
import { deepClone } from '../src/hash.ts';
import { buildSampleLog } from './helpers.ts';

test('分支拼接：基点前缀摘要匹配时合并成功', () => {
  const { records } = buildSampleLog();
  const forkAt = 2;
  const branch = forkLog(records, 'feature-x', forkAt);
  branch.append('b-op-1', { kind: 'kv.set', key: 'feature:x', value: true });
  branch.append('b-op-2', { kind: 'kv.add', key: 'balance:ada', delta: 5 });

  const merged = spliceBranch(records, branch.records);
  assert.equal(merged.length, forkAt + branch.records.length);
  assert.equal(verifyChain(merged).ok, true, '拼接后的链必须完整自洽');

  const truth = fullLogInterpret(merged);
  const itp = new Interpreter(merged);
  itp.runToEnd();
  assert.equal(itp.summary(), truth.summary);
  assert.equal(itp.getState()['feature:x'], true);
  assert.equal(itp.getState()['balance:ada'], 105);
});

test('前缀摘要不符：拒绝拼接', () => {
  const { records } = buildSampleLog();
  const branch = forkLog(records, 'feature-y', 2);
  branch.append('b-op-1', { kind: 'kv.set', key: 'y', value: 1 });
  const forged = deepClone(branch.records);
  const head = forged[0];
  assert.equal(head.body.kind, 'branch');
  if (head.body.kind === 'branch') head.body.baseHash = 'aa'.repeat(32); // 伪造基点摘要
  assert.throws(() => spliceBranch(records, forged), SpliceError);
});

test('基点越界与非分支首条：拒绝拼接', () => {
  const { records } = buildSampleLog();
  const branch = forkLog(records, 'feature-z', 2);
  branch.append('b-op-1', { kind: 'kv.set', key: 'z', value: 1 });
  const noHead = branch.records.slice(1);
  assert.throws(() => spliceBranch(records, noHead), SpliceError);

  assert.throws(() => forkLog(records, 'feature-w', records.length + 10), /非法分叉点/);
  const outOfRange = deepClone(branch.records);
  if (outOfRange[0].body.kind === 'branch') outOfRange[0].body.baseSeq = records.length + 10;
  assert.throws(() => spliceBranch(records, outOfRange), SpliceError);
});

test('拼接到错误的主干：前缀摘要不符必须拒绝', () => {
  const a = buildSampleLog();
  const b = buildSampleLog(); // 相同脚本但独立链（哈希相同 → 换一条不同的）
  const branch = forkLog(a.records, 'feature-q', 2);
  branch.append('b-op-1', { kind: 'kv.set', key: 'q', value: 1 });
  // 构造一条不同的主干
  const other = forkLog(b.records, 'trunk2', 0);
  other.append('t-op-1', { kind: 'kv.set', key: 'other', value: 1 });
  assert.throws(() => spliceBranch(other.records, branch.records), SpliceError);
});
