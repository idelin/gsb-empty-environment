import { test } from 'node:test';
import assert from 'node:assert/strict';
import { GlobalReadViewAligner } from '../src/aligner.ts';
import { Shard } from '../src/shard.ts';

const SHARDS = ['shard-1', 'shard-2'];
const INITIAL = { A: 100, B: 50 }; // A 在分库1，B 在分库2，总资金 150
const TOTAL = 150;

function makeAligner() {
  return new GlobalReadViewAligner(SHARDS, INITIAL);
}

test('未闭合前阻止生成查询视图：只有转出事件时绝对阻塞', () => {
  const s1 = new Shard('shard-1');
  const s2 = new Shard('shard-2');
  const aligner = makeAligner();

  // 转账 txn-1：A(分库1) -> B(分库2)，金额 30
  const debit = s1.debitEvent('txn-1', 'A', 30, 2, SHARDS);
  aligner.ingest(debit); // 转出先到，转入（分库2）尚未就绪

  // 两个分片都已心跳，水位线均越过 debit 的 commitTs
  aligner.heartbeat({ shardId: 'shard-1', ts: 10 });
  aligner.heartbeat({ shardId: 'shard-2', ts: 10 });

  const view = aligner.getReadView();
  assert.equal(view.status, 'blocked');
  if (view.status === 'blocked') {
    assert.deepEqual(view.pendingTxns, ['txn-1']);
    assert.match(view.reason, /未因果闭合/);
  }
  // 阻塞期间绝不允许暴露 A=70,B=50（总额 120，钱凭空消失）的视图
});

test('转入转出全部就绪后输出一致性快照，资金守恒', () => {
  const s1 = new Shard('shard-1');
  const s2 = new Shard('shard-2');
  const aligner = makeAligner();

  const debit = s1.debitEvent('txn-1', 'A', 30, 2, SHARDS);
  aligner.ingest(debit);
  aligner.heartbeat({ shardId: 'shard-1', ts: 10 });
  aligner.heartbeat({ shardId: 'shard-2', ts: 10 });
  assert.equal(aligner.getReadView().status, 'blocked'); // 闭合前仍阻塞

  // 分库2 的转入事件就绪（异步延迟到达）
  const credit = s2.creditEvent('txn-1', 'B', 30, 2, SHARDS);
  aligner.ingest(credit);
  aligner.heartbeat({ shardId: 'shard-2', ts: 20 });

  const view = aligner.getReadView();
  assert.equal(view.status, 'ready');
  if (view.status === 'ready') {
    assert.deepEqual(view.balances, { A: 70, B: 80 });
    assert.deepEqual(view.closedTxns, ['txn-1']);
    const total = Object.values(view.balances).reduce((a, b) => a + b, 0);
    assert.equal(total, TOTAL, '资金必须守恒，杜绝钱凭空消失');
  }
});

test('分片未上报水位线时阻塞（无法保证事件完整性）', () => {
  const s1 = new Shard('shard-1');
  const aligner = makeAligner();

  aligner.ingest(s1.debitEvent('txn-1', 'A', 30, 2, SHARDS));
  aligner.heartbeat({ shardId: 'shard-1', ts: 10 });
  // shard-2 从未心跳

  const view = aligner.getReadView();
  assert.equal(view.status, 'blocked');
  if (view.status === 'blocked') {
    assert.match(view.reason, /水位线/);
  }
});

test('T_Safe 不回放半事务：闭合前 safeTs 必须小于事务首个事件时间戳', () => {
  const s1 = new Shard('shard-1');
  const aligner = makeAligner();

  const debit = s1.debitEvent('txn-1', 'A', 30, 2, SHARDS); // commitTs = 1
  aligner.ingest(debit);
  aligner.heartbeat({ shardId: 'shard-1', ts: 100 });
  aligner.heartbeat({ shardId: 'shard-2', ts: 100 });

  const safeTs = aligner.computeSafeTs();
  assert.ok(safeTs !== undefined && safeTs < debit.commitTs,
    `safeTs(${safeTs}) 必须 < 未闭合事务首事件 commitTs(${debit.commitTs})`);
});

test('多笔并发转账：全部闭合后快照自洽且总资金守恒', () => {
  const s1 = new Shard('shard-1');
  const s2 = new Shard('shard-2');
  const aligner = makeAligner();

  // txn-1: A -> B 30；txn-2: B -> A 10（反向）
  aligner.ingest(s1.debitEvent('txn-1', 'A', 30, 2, SHARDS));
  aligner.ingest(s2.debitEvent('txn-2', 'B', 10, 2, SHARDS));
  aligner.heartbeat({ shardId: 'shard-1', ts: 50 });
  aligner.heartbeat({ shardId: 'shard-2', ts: 50 });
  assert.equal(aligner.getReadView().status, 'blocked'); // 两笔都只有半边

  aligner.ingest(s2.creditEvent('txn-1', 'B', 30, 2, SHARDS));
  assert.equal(aligner.getReadView().status, 'blocked'); // txn-2 仍未闭合

  aligner.ingest(s1.creditEvent('txn-2', 'A', 10, 2, SHARDS));
  aligner.heartbeat({ shardId: 'shard-1', ts: 100 });
  aligner.heartbeat({ shardId: 'shard-2', ts: 100 });

  const view = aligner.getReadView();
  assert.equal(view.status, 'ready');
  if (view.status === 'ready') {
    assert.deepEqual(view.balances, { A: 80, B: 70 });
    const total = Object.values(view.balances).reduce((a, b) => a + b, 0);
    assert.equal(total, TOTAL);
    assert.equal(view.closedTxns.length, 2);
  }
});

test('事件乱序到达（转入先于转出）同样保证闭合语义', () => {
  const s1 = new Shard('shard-1');
  const s2 = new Shard('shard-2');
  const aligner = makeAligner();

  // 先造好两个事件，但按 credit -> debit 顺序投递
  const debit = s1.debitEvent('txn-1', 'A', 30, 2, SHARDS);
  const credit = s2.creditEvent('txn-1', 'B', 30, 2, SHARDS);

  aligner.ingest(credit); // 转入先到
  aligner.heartbeat({ shardId: 'shard-1', ts: 10 });
  aligner.heartbeat({ shardId: 'shard-2', ts: 10 });
  assert.equal(aligner.getReadView().status, 'blocked'); // 只有转入也不许可见

  aligner.ingest(debit);
  const view = aligner.getReadView();
  assert.equal(view.status, 'ready');
  if (view.status === 'ready') {
    assert.deepEqual(view.balances, { A: 70, B: 80 });
  }
});
