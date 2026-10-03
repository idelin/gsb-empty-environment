import { mkdtemp, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import { setImmediate as yieldLoop } from 'node:timers/promises';
import { StateTree } from '../src/stateTree.ts';
import { Checkpointer } from '../src/checkpointer.ts';
import { restore, CheckpointCorruptionError } from '../src/recovery.ts';

const dir = await mkdtemp(path.join(tmpdir(), 'chkpt-demo-'));
const tree = new StateTree(1024);
const chk = new Checkpointer(tree, dir, { pagesPerChunk: 32 });

async function writeLoad(target: StateTree, trigger?: () => void): Promise<number[]> {
  const lat: number[] = [];
  for (let i = 0; i < 100_000; i++) {
    const t0 = performance.now();
    target.set(`user:${i}`, `profile-${i}`);
    lat.push(performance.now() - t0);
    if (i % 500 === 499) await yieldLoop();
    if (i === 50_000) trigger?.();
  }
  return lat.sort((a, b) => a - b);
}
const report = (label: string, lat: number[]) => {
  const pct = (p: number) => lat[Math.ceil((p / 100) * lat.length) - 1].toFixed(3);
  console.log(`   ${label}: p50=${pct(50)}ms p99=${pct(99)}ms max=${lat[lat.length - 1].toFixed(3)}ms`);
};

console.log('== 1. 连续写入 100,000 个键值对，中途触发全量检查点 ==');
report('基线(无检查点)  ', await writeLoad(new StateTree(1024)));
let cp: Promise<unknown> | null = null;
const lat = await writeLoad(tree, () => { cp = chk.checkpoint(); });
const manifest = (await cp) as Awaited<ReturnType<typeof chk.checkpoint>>;
report('写入+并发检查点 ', lat);
console.log(`   检查点 ${manifest.id}: ${manifest.chunks.length} 个分块, manifestHash=${manifest.manifestHash.slice(0, 16)}…`);

await chk.checkpoint(); // 覆盖写入尾部，形成完整基线
console.log('== 2. 再修改 1,000 个键，生成增量差量包 ==');
for (let i = 0; i < 1_000; i++) tree.set(`user:${i}`, `profile-v2-${i}`);
const incr = await chk.checkpoint();
console.log(`   增量 ${incr.id} (${incr.type}): 仅 ${incr.chunks.reduce((n, c) => n + c.pageCount, 0)} 个脏页 / ${incr.chunks.length} 个分块`);

console.log('== 3. 正常恢复并校验哈希链 ==');
const ok = await restore(dir);
console.log(`   恢复成功: ${ok.state.size} 个键, 校验 ${ok.chunksVerified} 块 / ${ok.pagesVerified} 页`);

console.log('== 4. 损坏第 3 个分块后重试恢复 ==');
const victim = manifest.chunks[2];
const victimPath = path.join(dir, victim.file);
const buf = await readFile(victimPath);
buf[100] ^= 0xff;
await writeFile(victimPath, buf);
try {
  await restore(dir);
  console.log('   意外：损坏数据被接受！');
} catch (err) {
  if (err instanceof CheckpointCorruptionError) {
    console.log(`   恢复拒绝坏数据 ✔  定位: checkpoint=${err.details.checkpointId} chunk=#${err.details.chunkIndex} file=${err.details.file}`);
  } else {
    throw err;
  }
}

await rm(dir, { recursive: true, force: true });
