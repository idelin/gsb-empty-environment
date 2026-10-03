/**
 * 并发工作负载：N 个读任务 + M 个写任务同时攻击同一棵 B+ 树。
 * 测试与演示共用，保证"运行样例"与"验收测试"行为一致。
 */

import { BPlusTree } from "./bplustree.ts";

/** 确定性伪随机数（LCG），保证测试可复现 */
export function makeRng(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
    return state / 0x100000000;
  };
}

export interface WorkloadOptions {
  readerCount: number;
  writerCount: number;
  keysPerWriter: number;
  /** 读线程随机探测的 key 空间大小 */
  keySpace: number;
  seed?: number;
}

export interface WorkloadResult {
  insertedKeys: number;
  totalReads: number;
  hits: number;
  misses: number;
  elapsedMs: number;
}

export async function runConcurrentWorkload(
  tree: BPlusTree,
  options: WorkloadOptions,
): Promise<WorkloadResult> {
  const { readerCount, writerCount, keysPerWriter, keySpace } = options;
  const rng = makeRng(options.seed ?? 42);

  let writersDone = false;
  let totalReads = 0;
  let hits = 0;

  // 写线程：各自插入一段不相交的、打乱顺序的 key
  const writers = Array.from({ length: writerCount }, (_, w) =>
    (async () => {
      const keys: number[] = [];
      for (let i = 0; i < keysPerWriter; i++) keys.push(w * keysPerWriter + i);
      // Fisher-Yates 洗牌（用各写线程私有的确定性 rng）
      const local = makeRng((options.seed ?? 42) * 1000 + w);
      for (let i = keys.length - 1; i > 0; i--) {
        const j = Math.floor(local() * (i + 1));
        [keys[i], keys[j]] = [keys[j]!, keys[i]!];
      }
      for (const k of keys) await tree.insert(k);
    })(),
  );

  // 读线程：写线程全部完成前持续随机探测
  const readers = Array.from({ length: readerCount }, () =>
    (async () => {
      while (!writersDone) {
        const key = Math.floor(rng() * keySpace);
        if (await tree.find(key)) hits++;
        totalReads++;
      }
    })(),
  );

  const start = performance.now();
  await Promise.all(writers);
  writersDone = true;
  await Promise.all(readers);
  const elapsedMs = performance.now() - start;

  return {
    insertedKeys: writerCount * keysPerWriter,
    totalReads,
    hits,
    misses: totalReads - hits,
    elapsedMs,
  };
}
