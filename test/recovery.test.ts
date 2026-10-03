import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FaultySink, LossyChannel, MemorySink } from '../src/fault.ts';
import { Journal, RECORD_HEADER_SIZE, encodeRecord, scanRecords } from '../src/journal.ts';
import { TransactionalStore, reliableTransfer } from '../src/store.ts';
import { crc32 } from '../src/crc32.ts';
import { isProtoError } from '../src/errors.ts';

const enc = (s: string) => new TextEncoder().encode(s);

test('crc32 与已知向量一致', () => {
  assert.equal(crc32(enc('123456789')), 0xcbf43926);
  assert.equal(crc32(new Uint8Array(0)), 0);
});

test('日志记录往返与扫描', () => {
  const sink = new MemorySink();
  const j = new Journal(sink);
  j.append(enc('txn-1'));
  j.append(enc('txn-2'));
  const payloads = j.recover().map((p) => new TextDecoder().decode(p));
  assert.deepEqual(payloads, ['txn-1', 'txn-2']);
});

test('断电（撕裂写）：自愈截断到最后完好记录', () => {
  const inner = new MemorySink();
  const sink = new FaultySink(inner);
  const j = new Journal(sink);
  j.append(enc('committed-1'));
  j.append(enc('committed-2'));
  const sizeBefore = sink.size;

  // 断电：第三条记录只写入部分字节
  sink.plan.powerLossAfterBytes = RECORD_HEADER_SIZE + 2;
  assert.throws(() => j.append(enc('partial-txn')), (e: unknown) => isProtoError(e, 'POWER_LOSS'));
  assert.equal(sink.stats.powerLoss, 1);
  assert.ok(sink.size > sizeBefore, '撕裂字节已落盘');

  // 崩溃恢复：重放完好记录并截断撕裂尾部
  const payloads = j.recover().map((p) => new TextDecoder().decode(p));
  assert.deepEqual(payloads, ['committed-1', 'committed-2']);
  assert.equal(sink.size, sizeBefore, '文件已自愈截断');

  // 自愈后可继续写入
  j.append(enc('txn-3'));
  assert.equal(j.recover().length, 3);
});

test('校验和损坏：扫描报告 repaired 并停在损坏点', () => {
  const rec1 = encodeRecord(enc('good'));
  const rec2 = encodeRecord(enc('tampered'));
  rec2[rec2.length - 1]! ^= 0xff; // 翻转末字节
  const data = new Uint8Array(rec1.length + rec2.length);
  data.set(rec1, 0);
  data.set(rec2, rec1.length);
  const { payloads, goodLength, repaired } = scanRecords(data);
  assert.equal(payloads.length, 1);
  assert.equal(goodLength, rec1.length);
  assert.ok(repaired);
});

test('磁盘满：commit 失败且状态完全回滚、无 staged 泄漏', async () => {
  const sink = new FaultySink(new MemorySink());
  const store = new TransactionalStore(new Journal(sink));
  await store.commit([{ kind: 'put', key: 'a', value: enc('1') }]);
  assert.equal(store.size, 1);

  sink.plan.diskFull = true;
  await assert.rejects(
    store.commit([
      { kind: 'put', key: 'b', value: enc('2') },
      { kind: 'del', key: 'a' },
    ]),
    (e: unknown) => isProtoError(e, 'DISK_FULL'),
  );
  assert.equal(sink.stats.diskFull, 1);
  // 状态回滚：b 未写入，a 未删除
  assert.equal(store.get('b'), undefined);
  assert.deepEqual(store.get('a'), enc('1'));
  assert.equal(store.stagedBufferCount, 0, 'staged 缓冲已全部释放');

  // 故障解除后可恢复写入
  sink.plan.diskFull = false;
  await store.commit([{ kind: 'put', key: 'b', value: enc('2') }]);
  assert.deepEqual(store.get('b'), enc('2'));
  assert.equal(store.stagedBufferCount, 0);
});

test('断电中断 commit：恢复后状态与日志一致', async () => {
  const sink = new FaultySink(new MemorySink());
  const store = new TransactionalStore(new Journal(sink));
  await store.commit([{ kind: 'put', key: 'k1', value: enc('v1') }]);

  sink.plan.powerLossAfterBytes = 3; // 事务记录写到一半断电
  await assert.rejects(
    store.commit([{ kind: 'put', key: 'k2', value: enc('v2') }]),
    (e: unknown) => isProtoError(e, 'POWER_LOSS'),
  );
  assert.equal(store.get('k2'), undefined, '内存状态未应用半截事务');

  // 模拟重启：新 store 从日志恢复
  const store2 = new TransactionalStore(new Journal(sink));
  const replayed = store2.recover();
  assert.equal(replayed, 1, '只重放完好事务');
  assert.deepEqual(store2.get('k1'), enc('v1'));
  assert.equal(store2.get('k2'), undefined);
});

test('丢包网络：分片重传后精确重组', async () => {
  const data = new Uint8Array(1000);
  for (let i = 0; i < data.length; i++) data[i] = i & 0xff;
  const channel = new LossyChannel([1, 3, 5, 7]); // 第 1/3/5/7 次发送丢包
  const { chunks, received } = await reliableTransfer(data, 100, channel);
  assert.equal(chunks, 10);
  assert.deepEqual(received, data);
  assert.equal(channel.stats.dropped, 4);
  assert.equal(channel.stats.retransmitted, 4);
});

test('丢包超过重传上限 -> PACKET_LOSS', async () => {
  const channel = new LossyChannel(Array.from({ length: 50 }, (_, i) => i + 1)); // 永远丢包
  await assert.rejects(
    reliableTransfer(new Uint8Array(10), 5, channel, 5),
    (e: unknown) => isProtoError(e, 'PACKET_LOSS'),
  );
});
