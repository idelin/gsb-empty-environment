/**
 * 运行样例：10 个读任务 + 5 个写任务并发访问同一棵螃蟹脚 B+ 树。
 * 运行：npm run demo
 */
import { BPlusTree } from "./bptree.ts";
import { latchStats } from "./latch.ts";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

function mulberry32(seed: number) {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const ORDER = 4;              // 小阶数 -> 树高、分裂频繁，压力更大
const READERS = 10;
const WRITERS = 5;
const INSERTS_PER_WRITER = 400;
const READS_PER_READER = 2000;
const KEY_SPACE = 1000 + WRITERS * INSERTS_PER_WRITER;

async function main() {
  const tree = new BPlusTree<string>(ORDER);
  for (let i = 0; i < 1000; i++) await tree.insert(i, `val-${i}`);
  console.log(`种子数据 1000 键完成，树高 = ${tree.height()}`);

  latchStats.reset();
  const rand = mulberry32(20261002);
  const start = performance.now();

  const writers = Array.from({ length: WRITERS }, (_, w) => (async () => {
    for (let i = 0; i < INSERTS_PER_WRITER; i++) {
      const key = 1000 + w * INSERTS_PER_WRITER + i;
      await tree.insert(key, `val-${key}`);
      if (i % 5 === 0) await sleep(0); // 主动让出，制造交错
    }
  })());

  const readers = Array.from({ length: READERS }, () => (async () => {
    let hits = 0;
    for (let i = 0; i < READS_PER_READER; i++) {
      const key = Math.floor(rand() * KEY_SPACE);
      const v = await tree.get(key);
      if (v !== undefined && v !== `val-${key}`) {
        throw new Error(`数据损坏: key=${key} value=${v}`);
      }
      if (v !== undefined) hits++;
      if (i % 50 === 0) await sleep(0);
    }
    return hits;
  })());

  const hits = await Promise.all([...writers, ...readers].map((p, i) => p));
  const elapsed = performance.now() - start;

  // 校验：所有写入键都在，且中序遍历严格递增
  let missing = 0;
  for (let k = 0; k < KEY_SPACE; k++) if (!(await tree.has(k))) missing++;
  const entries = tree.collect();
  const sorted = entries.every((e, i) => i === 0 || entries[i - 1][0] < e[0]);

  console.log(`\n=== 并发完成（无死锁） ===`);
  console.log(`耗时:            ${elapsed.toFixed(1)} ms`);
  console.log(`树高:            ${tree.height()}  键总数: ${entries.length}`);
  console.log(`读者总命中:      ${(hits as number[]).slice(WRITERS).reduce((a, b) => a + b, 0)}`);
  console.log(`缺失键:          ${missing}  中序有序: ${sorted}`);
  console.log(`读锁获取:        ${latchStats.readAcquires}  (其中排队等待 ${latchStats.readWaits})`);
  console.log(`写锁获取:        ${latchStats.writeAcquires}  (其中排队等待 ${latchStats.writeWaits})`);
  console.log(`最大并发读者数:  ${latchStats.maxConcurrentReaders}  (>1 说明读者在高速层无阻塞并行穿越)`);
}

await main();
