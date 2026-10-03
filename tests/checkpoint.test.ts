import { test } from 'node:test';
import { setImmediate as yieldLoop } from 'node:timers/promises';
import assert from 'node:assert/strict';
import { mkdtemp, rm, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import { StateTree } from '../src/stateTree.ts';
import { Checkpointer } from '../src/checkpointer.ts';
import { restore, CheckpointCorruptionError } from '../src/recovery.ts';

async function tempDir(): Promise<string> {
  return mkdtemp(path.join(tmpdir(), 'chkpt-'));
}

function percentile(sorted: number[], p: number): number {
  const idx = Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1);
  return sorted[idx];
}

test('增量检查点只包含脏页，恢复后状态一致', async () => {
  const dir = await tempDir();
  try {
    const tree = new StateTree(64);
    const chk = new Checkpointer(tree, dir, { pagesPerChunk: 8 });

    for (let i = 0; i < 2000; i++) tree.set(`key-${i}`, `value-${i}`);
    const full = await chk.checkpoint();
    assert.equal(full.type, 'full');

    // 只改 10 个 key → 增量差量包应远小于全量
    for (let i = 0; i < 10; i++) tree.set(`key-${i}`, `updated-${i}`);
    const incr = await chk.checkpoint();
    assert.equal(incr.type, 'incr');
    const incrPages = incr.chunks.reduce((n, c) => n + c.pageCount, 0);
    const fullPages = full.chunks.reduce((n, c) => n + c.pageCount, 0);
    assert.ok(incrPages <= 10, `incr pages ${incrPages} should be <= 10`);
    assert.ok(incrPages < fullPages / 4);

    const result = await restore(dir);
    assert.equal(result.state.size, 2000);
    assert.equal(result.state.get('key-0')!.toString(), 'updated-0');
    assert.equal(result.state.get('key-1999')!.toString(), 'value-1999');
    assert.deepEqual(result.checkpointsApplied, [full.id, incr.id]);
    assert.ok(result.chunksVerified >= full.chunks.length);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('验收：连续写入 10 万键值期间触发检查点，写入延迟抖动受控', async () => {
  const dir = await tempDir();
  const TOTAL = 100_000;

  async function writeLoad(tree: StateTree, trigger?: () => void): Promise<number[]> {
    const lat: number[] = [];
    for (let i = 0; i < TOTAL; i++) {
      const start = performance.now();
      tree.set(`user:${i}`, `payload-${i}-${'x'.repeat(32)}`);
      lat.push(performance.now() - start);
      // 模拟真实写入路径的异步边界，让检查点与写入真正并发
      if (i % 500 === 499) await yieldLoop();
      if (i === Math.floor(TOTAL / 2)) trigger?.();
    }
    return lat.sort((a, b) => a - b);
  }
  const stats = (lat: number[]) => ({
    p50: percentile(lat, 50),
    p99: percentile(lat, 99),
    max: lat[lat.length - 1],
  });

  try {
    // 基线：同样的 10 万写入，不触发检查点
    const baseline = stats(await writeLoad(new StateTree(1024)));

    // 对照：写入中途触发全量检查点
    const tree = new StateTree(1024);
    const chk = new Checkpointer(tree, dir, { pagesPerChunk: 32, yieldEveryPages: 8 });
    let checkpointPromise: Promise<unknown> | null = null;
    const underCheckpoint = stats(
      await writeLoad(tree, () => {
        checkpointPromise = chk.checkpoint();
      }),
    );
    assert.ok(checkpointPromise, 'checkpoint should have been triggered');
    const manifest = (await checkpointPromise) as Awaited<ReturnType<typeof chk.checkpoint>>;
    assert.equal(manifest.type, 'full');

    console.log(
      `    [latency] baseline:          p50=${baseline.p50.toFixed(3)}ms p99=${baseline.p99.toFixed(3)}ms max=${baseline.max.toFixed(3)}ms`,
    );
    console.log(
      `    [latency] with checkpoint:   p50=${underCheckpoint.p50.toFixed(3)}ms p99=${underCheckpoint.p99.toFixed(3)}ms max=${underCheckpoint.max.toFixed(3)}ms`,
    );

    // 抖动受控：稳态尾延迟(p99)在毫秒级以下；最大停顿不显著超出环境基线噪声
    assert.ok(underCheckpoint.p99 < 1, `p99 ${underCheckpoint.p99.toFixed(3)}ms out of control`);
    assert.ok(
      underCheckpoint.max <= Math.max(50, baseline.max * 3),
      `max ${underCheckpoint.max.toFixed(3)}ms vs baseline ${baseline.max.toFixed(3)}ms out of control`,
    );

    // 写入结束后补一次增量检查点，日志链 = 全量 + 增量
    const tail = await chk.checkpoint();
    assert.equal(tail.type, 'incr');

    // 检查点生成期间写入未中断，且恢复后数据完整
    assert.equal(tree.size, TOTAL);
    const result = await restore(dir);
    assert.equal(result.state.size, TOTAL);
    for (const i of [0, 12345, TOTAL / 2, TOTAL - 1]) {
      assert.equal(result.state.get(`user:${i}`)!.toString(), `payload-${i}-${'x'.repeat(32)}`);
    }
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('验收：损坏第 3 个分块，恢复流程精确定位并拒绝坏数据', async () => {
  const dir = await tempDir();
  try {
    const tree = new StateTree(256);
    // pagesPerChunk=4 → 足够多的分块，保证存在第 3 块
    const chk = new Checkpointer(tree, dir, { pagesPerChunk: 4 });
    for (let i = 0; i < 5000; i++) tree.set(`k${i}`, `v${i}`);
    const manifest = await chk.checkpoint();
    assert.ok(manifest.chunks.length >= 3, 'need at least 3 chunks');

    // 破坏第 3 个分块（翻转文件中部一个字节）
    const victim = manifest.chunks[2];
    assert.equal(victim.index, 3);
    const filePath = path.join(dir, victim.file);
    const buf = await readFile(filePath);
    buf[Math.floor(buf.length / 2)] ^= 0xff;
    await writeFile(filePath, buf);

    const err: unknown = await restore(dir).then(
      () => null,
      (e: unknown) => e,
    );
    assert.ok(err instanceof CheckpointCorruptionError, 'should reject with CheckpointCorruptionError');
    assert.equal(err.details.chunkIndex, 3, 'should pinpoint chunk #3');
    assert.equal(err.details.checkpointId, manifest.id);
    assert.equal(err.details.file, victim.file);
    assert.notEqual(err.details.expected, err.details.actual);
    console.log(`    [corruption] precisely located: ${err.message}`);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('清单被篡改时哈希链校验失败', async () => {
  const dir = await tempDir();
  try {
    const tree = new StateTree(32);
    const chk = new Checkpointer(tree, dir, { pagesPerChunk: 4 });
    for (let i = 0; i < 500; i++) tree.set(`a${i}`, `b${i}`);
    const m1 = await chk.checkpoint();
    tree.set('a0', 'tamper-trigger');
    await chk.checkpoint();

    // 篡改第一个清单中的页哈希
    const m1Path = path.join(dir, `${m1.id}.manifest.json`);
    const tampered = JSON.parse(await readFile(m1Path, 'utf8'));
    const somePage = Object.keys(tampered.pages)[0];
    tampered.pages[somePage].hash = 'f'.repeat(64);
    await writeFile(m1Path, JSON.stringify(tampered, null, 2));

    await assert.rejects(restore(dir), CheckpointCorruptionError);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
