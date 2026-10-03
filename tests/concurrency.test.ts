import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DBSystem } from '../src/system.ts';
import { cleanup, crashAndReopen, recordsOf, tempDir, walRecords } from './helpers.ts';

const yieldNow = () => new Promise((r) => setImmediate(r));

async function increment(
  db: DBSystem,
  txId: number,
  pageId: string,
  delta: number,
): Promise<void> {
  const cur = await db.tx.read(txId, pageId);
  await yieldNow(); // 制造交错窗口；严格 2PL 保证读-改-写原子性
  await db.tx.update(txId, pageId, String(Number(cur || '0') + delta));
}

test('并发: 20 个事务并发递增共享计数器，无丢失更新', async () => {
  const dir = tempDir();
  try {
    const db = DBSystem.open(dir);
    const N = 20;
    await Promise.all(
      Array.from({ length: N }, async () => {
        const txId = db.tx.begin();
        // 固定页序加锁（C1 先于 C2），避免死锁
        await increment(db, txId, 'C1', 1);
        await increment(db, txId, 'C2', 2);
        await db.tx.commit(txId);
      }),
    );
    assert.equal(db.bufferPool.fetch('C1').data, String(N));
    assert.equal(db.bufferPool.fetch('C2').data, String(N * 2));
    assert.equal(db.lockManager.heldCount, 0, '锁无泄漏');
    assert.equal(db.tx.txTable.size, 0, '事务表无泄漏');
    // 每个事务都有完整的 COMMIT+END
    for (let txId = 1; txId <= N; txId++) {
      const types = recordsOf(db, txId).map((r) => r.type);
      assert.ok(types.includes('COMMIT') && types.at(-1) === 'END');
    }
  } finally {
    cleanup(dir);
  }
});

test('并发: 提交与中止混合，最终值只反映已提交增量', async () => {
  const dir = tempDir();
  try {
    const db = DBSystem.open(dir);
    const N = 30;
    const shouldCommit = (i: number) => i % 3 !== 0; // 1/3 中止
    await Promise.all(
      Array.from({ length: N }, async (_, i) => {
        const txId = db.tx.begin();
        await increment(db, txId, 'CNT', 10);
        await yieldNow();
        if (shouldCommit(i)) await db.tx.commit(txId);
        else await db.tx.abort(txId);
      }),
    );
    const committed = Array.from({ length: N }, (_, i) => i).filter(shouldCommit).length;
    assert.equal(db.bufferPool.fetch('CNT').data, String(committed * 10));
    assert.equal(db.lockManager.heldCount, 0);
    assert.equal(db.tx.txTable.size, 0);
    // 中止事务的每条 UPDATE 都有对应 CLR
    const recs = walRecords(db);
    const abortedUpdates = recs.filter(
      (r) => r.type === 'UPDATE' && !shouldCommit(r.txId - 1),
    );
    const clrs = recs.filter((r) => r.type === 'CLR');
    assert.equal(clrs.length, abortedUpdates.length, '补偿恰好覆盖全部中止更新');
  } finally {
    cleanup(dir);
  }
});

test('并发: 高并发负载中崩溃，恢复后每个事务要么完整生效要么完全消失', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const N = 24;
    // 并发推进所有事务的更新；一半提交、一半留在运行态即崩溃
    const txs = Array.from({ length: N }, () => db.tx.begin());
    await Promise.all(
      txs.map(async (txId, i) => {
        if (i % 2 === 0) {
          // 偶数事务：更新共享计数器并提交（奇数事务永不提交，不得阻塞它们）
          await increment(db, txId, 'SUM', 1);
        }
        await increment(db, txId, `OWN${i}`, 1);
        if (i % 2 === 0) {
          await db.tx.commit(txId);
        }
      }),
    );
    db = crashAndReopen(db);
    const report = db.recover();
    assert.equal(report.losers.length, N / 2, '奇数索引事务为 loser');

    // 最终一致性：SUM == 已提交事务数；loser 的私有页必须回滚
    assert.equal(db.readPersistedPage('SUM').data, String(N / 2));
    for (let i = 0; i < N; i++) {
      const want = i % 2 === 0 ? '1' : '';
      assert.equal(db.readPersistedPage(`OWN${i}`).data, want, `OWN${i}`);
    }
    // 无泄漏：WAL 中每个事务都以 END 收尾
    for (const txId of txs) {
      assert.equal(recordsOf(db, txId).at(-1)!.type, 'END');
    }
    assert.equal(db.lockManager.heldCount, 0);
  } finally {
    cleanup(dir);
  }
});

test('并发: 同页高竞争下锁 FIFO 互斥，无脏读脏写', async () => {
  const dir = tempDir();
  try {
    const db = DBSystem.open(dir);
    const N = 50;
    await Promise.all(
      Array.from({ length: N }, async () => {
        const txId = db.tx.begin();
        await increment(db, txId, 'HOT', 1);
        await db.tx.commit(txId);
      }),
    );
    assert.equal(db.bufferPool.fetch('HOT').data, String(N), '无丢失更新');
    assert.equal(db.lockManager.heldCount, 0);
  } finally {
    cleanup(dir);
  }
});
