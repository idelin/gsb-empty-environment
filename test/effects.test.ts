// 副作用：回放只触发未确认的外部动作；确认记录跨崩溃、跨检查点生效。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Interpreter } from '../src/interpreter.ts';
import { recover, createCheckpoint } from '../src/checkpoint.ts';
import { LogWriter } from '../src/log.ts';
import { buildSampleLog } from './helpers.ts';

test('已确认的副作用不重新触发，未确认的重放一次', () => {
  const { records } = buildSampleLog();
  const fired: string[] = [];
  new Interpreter(records, { onEffect: (eid) => fired.push(eid) }).runToEnd();
  assert.deepEqual(fired, ['eff-points-1'], 'eff-email-1 已确认不得重发；eff-points-1 未确认必须重发');
});

test('事务中的副作用在提交时才触发，中止则不触发', () => {
  const w = new LogWriter();
  w.append('op-1', { kind: 'kv.set', key: 'a', value: 1 });
  w.beginTx('tx-1');
  w.append('op-2', { kind: 'kv.set', key: 'b', value: 2 }, 'eff-in-tx');
  const fired: string[] = [];
  const itp = new Interpreter(w.records, { onEffect: (eid) => fired.push(eid) });
  itp.runToEnd();
  assert.deepEqual(fired, [], '提交前不得触发');

  const w2 = new LogWriter();
  w2.append('op-1', { kind: 'kv.set', key: 'a', value: 1 });
  w2.beginTx('tx-1');
  w2.append('op-2', { kind: 'kv.set', key: 'b', value: 2 }, 'eff-in-tx');
  w2.commitTx('tx-1');
  const fired2: string[] = [];
  new Interpreter(w2.records, { onEffect: (eid) => fired2.push(eid) }).runToEnd();
  assert.deepEqual(fired2, ['eff-in-tx'], '提交时恰好触发一次');

  const w3 = new LogWriter();
  w3.beginTx('tx-9');
  w3.append('op-1', { kind: 'kv.set', key: 'x', value: 1 }, 'eff-aborted');
  w3.abortTx('tx-9');
  const fired3: string[] = [];
  new Interpreter(w3.records, { onEffect: (eid) => fired3.push(eid) }).runToEnd();
  assert.deepEqual(fired3, [], '中止事务的副作用不得触发');
});

test('副作用确认跨检查点恢复仍然生效', () => {
  const { records } = buildSampleLog();
  // 在确认记录之后建检查点，再从检查点增量恢复
  const confirmIdx = records.findIndex((r) => r.body.kind === 'effect');
  const prefixItp = new Interpreter(records.slice(0, confirmIdx + 1));
  prefixItp.runToEnd();
  const cp = createCheckpoint('cp-1', prefixItp);
  const fired: string[] = [];
  const { interpreter, usedCheckpoint } = recover(records, [cp], (eid) => fired.push(eid));
  assert.equal(usedCheckpoint?.id, 'cp-1');
  assert.deepEqual(fired, ['eff-points-1'], '检查点之后的恢复仍不得重发已确认副作用');
  assert.ok(interpreter.isConfirmed('eff-email-1'));
});

test('确认记录写在操作之前也生效（预取扫描）', () => {
  // 手工构造：先确认 eff-x，后出现携带 eff-x 的操作（乱序确认）
  const w = new LogWriter();
  w.confirmEffect('eff-x', 'pre-acked');
  w.append('op-1', { kind: 'kv.set', key: 'a', value: 1 }, 'eff-x');
  const fired: string[] = [];
  new Interpreter(w.records, { onEffect: (eid) => fired.push(eid) }).runToEnd();
  assert.deepEqual(fired, [], '任何位置的确认都必须抑制重复触发');
});
