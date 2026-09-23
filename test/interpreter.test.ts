// 解释器：逐步、反向步进（逆补丁）、取消、读者隔离、重复操作去重。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Interpreter } from '../src/interpreter.ts';
import { fullLogInterpret } from '../src/differential.ts';
import { LogWriter } from '../src/log.ts';
import { GENESIS, stateSummary } from '../src/hash.ts';
import { buildSampleLog, randomLog, mulberry32, rechain } from './helpers.ts';

test('逐步前进：每个前缀的状态摘要都与独立全日志解释器一致', () => {
  const { records } = buildSampleLog();
  const itp = new Interpreter(records);
  assert.equal(itp.prefixHash(), GENESIS);
  for (let i = 0; i < records.length; i++) {
    itp.step();
    const oracle = fullLogInterpret(records.slice(0, i + 1));
    assert.equal(itp.summary(), oracle.summary, `第 ${i + 1} 步后摘要必须一致`);
  }
  assert.equal(itp.step(), null); // 已到底
});

test('反向步进：逆补丁逐步撤销，每个位置都与独立解释器一致', () => {
  const { records } = buildSampleLog();
  const itp = new Interpreter(records);
  itp.runToEnd();
  for (let i = records.length; i >= 0; i--) {
    const oracle = fullLogInterpret(records.slice(0, i));
    assert.equal(itp.summary(), oracle.summary, `回退到 ${i} 后摘要必须一致`);
    if (i > 0) itp.stepBack();
  }
  assert.equal(itp.prefixHash(), GENESIS);
  assert.equal(itp.stepBack(), null); // 已到顶
});

test('随机前进/回退游走：随时与独立解释器对照', () => {
  const rng = mulberry32(42);
  const records = randomLog(rng, 60);
  const itp = new Interpreter(records);
  for (let moves = 0; moves < 400; moves++) {
    const pos = itp.position;
    const oracle = fullLogInterpret(records.slice(0, pos));
    assert.equal(itp.summary(), oracle.summary, `位置 ${pos} 摘要必须一致`);
    if (pos === 0 || (pos < records.length && rng() < 0.6)) itp.step();
    else itp.stepBack();
  }
});

test('读者不能看到事务中间态', () => {
  const { records } = buildSampleLog();
  const itp = new Interpreter(records);
  // 走到 tx-1 中间：begin + 第一条暂存 op
  while (true) {
    const rec = itp.step();
    assert.ok(rec);
    if (rec.body.kind === 'op' && rec.body.tx === 'tx-1') break;
  }
  const mid = itp.getState();
  assert.equal(mid['balance:ada'], 100, '事务未提交，读者必须看到旧值');
  assert.equal(mid['balance:bob'], 50);
  // 走完整个事务后可见
  itp.runToEnd();
  const end = itp.getState();
  assert.equal(end['balance:ada'], 70); // 100 - 30
  assert.equal(end['balance:bob'], 80);
  assert.equal(end['scratch:tmp'], undefined, '中止事务的写入不可见');
});

test('取消：丢弃未提交事务，状态回到事务前', () => {
  const w = new LogWriter();
  w.append('op-1', { kind: 'kv.set', key: 'a', value: 1 });
  w.beginTx('tx-1');
  w.append('op-2', { kind: 'kv.set', key: 'a', value: 2 });
  const itp = new Interpreter(w.records);
  itp.runToEnd();
  assert.equal(itp.hasOpenTx(), true);
  assert.equal(itp.getState()['a'], 1, '未提交事务不可见');
  assert.equal(itp.cancel(), true);
  assert.equal(itp.hasOpenTx(), false);
  assert.equal(itp.getState()['a'], 1);
  assert.equal(itp.cancel(), false, '没有打开的事务时取消为空操作');
});

test('重复操作 ID 不重放两次', () => {
  const { records, truth } = buildSampleLog();
  // 在链中插入一条 op-4 的重复记录（重链使其链上合法）
  const idx = records.findIndex((r) => r.body.kind === 'op' && r.body.opId === 'op-4');
  const dup = [...records.slice(0, idx + 1), records[idx], ...records.slice(idx + 1)];
  const dupLog = rechain(dup);
  const itp = new Interpreter(dupLog);
  itp.runToEnd();
  assert.equal(itp.summary(), truth.summary, '重复操作不得二次生效');
  assert.equal(itp.getState()['balance:bob'], 80, '转账不得重复入账');
});

test('缺中间操作不能跳过：断点之后的记录一律拒绝', () => {
  const { records } = buildSampleLog();
  const tailOnly = records.slice(3); // 直接从第 4 条开始
  assert.throws(() => new Interpreter(tailOnly).runToEnd(), /序号断裂|prev 指针/);
});

test('恢复后最终摘要等于原状态', () => {
  const { records, originalState, truth } = buildSampleLog();
  const itp = new Interpreter(records);
  itp.runToEnd();
  const expected = stateSummary(originalState);
  assert.equal(itp.summary(), expected);
  assert.equal(truth.summary, expected, '独立解释器基准也必须等于原状态');
});
