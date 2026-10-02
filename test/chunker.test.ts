import { test } from 'node:test';
import assert from 'node:assert/strict';
import { splitTransaction } from '../src/chunker.ts';
import { createHasher } from '../src/checksum.ts';
import type { Chunk, DataChunk, EndChunk } from '../src/types.ts';
import { generateRows } from './helpers.ts';

async function collect(iter: AsyncGenerator<Chunk>): Promise<Chunk[]> {
  const out: Chunk[] = [];
  for await (const chunk of iter) {
    out.push(chunk);
  }
  return out;
}

test('分片器：25,000 行按 5,000 切分为 5 个 DATA 分片，带 BEGIN/END 阶段标记', async () => {
  const chunks = await collect(splitTransaction('tx-1', generateRows(25_000), 5_000));

  assert.equal(chunks.length, 7);
  assert.equal(chunks[0].kind, 'BEGIN');
  assert.equal(chunks[6].kind, 'END');

  const dataChunks = chunks.slice(1, 6) as DataChunk[];
  assert.ok(dataChunks.every((c) => c.kind === 'DATA' && c.rowCount === 5_000));

  chunks.forEach((chunk, i) => assert.equal(chunk.seq, i, 'seq 必须连续递增'));

  const end = chunks[6] as EndChunk;
  assert.equal(end.totalRows, 25_000);
  assert.equal(end.totalChunks, 5);
});

test('分片器：尾批不足 chunkSize 时仍完整产出', async () => {
  const chunks = await collect(splitTransaction('tx-2', generateRows(12_345), 5_000));
  const dataChunks = chunks.filter((c) => c.kind === 'DATA') as DataChunk[];
  assert.deepEqual(dataChunks.map((c) => c.rowCount), [5_000, 5_000, 2_345]);
  const end = chunks[chunks.length - 1] as EndChunk;
  assert.equal(end.totalRows, 12_345);
  assert.equal(end.totalChunks, 3);
});

test('分片器：空事务只产生 BEGIN 与 END', async () => {
  const chunks = await collect(splitTransaction('tx-3', generateRows(0), 1_000));
  assert.equal(chunks.length, 2);
  assert.equal(chunks[0].kind, 'BEGIN');
  assert.equal(chunks[1].kind, 'END');
  assert.equal((chunks[1] as EndChunk).totalRows, 0);
});

test('分片器：分片校验和与全事务校验和可独立复算', async () => {
  const chunks = await collect(splitTransaction('tx-4', generateRows(1_000), 100));
  const txHasher = createHasher();
  for (const chunk of chunks) {
    if (chunk.kind !== 'DATA') continue;
    const chunkHasher = createHasher();
    for (const row of chunk.rows) {
      const s = JSON.stringify(row);
      chunkHasher.update(s);
      txHasher.update(s);
    }
    assert.equal(chunkHasher.digest(), chunk.checksum);
  }
  const end = chunks[chunks.length - 1] as EndChunk;
  assert.equal(txHasher.digest(), end.checksum);
});

test('分片器：非法 chunkSize 抛错', async () => {
  await assert.rejects(
    () => collect(splitTransaction('tx-5', generateRows(1), 0)),
    RangeError,
  );
});
