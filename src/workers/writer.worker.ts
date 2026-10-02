/**
 * 写线程：持续密集执行“转账”写事务（A 扣款、B 加款，同一 txId 提交），
 * 保证全表总和守恒。写路径无任何等待，提交速率即压测指标。
 */
import { parentPort, workerData } from 'node:worker_threads';
import { MvccStore } from '../store.ts';

const { sab, keyCount, poolCapacity, commits, initialValue, seed } = workerData as {
  sab: SharedArrayBuffer;
  keyCount: number;
  poolCapacity: number;
  commits: number;
  initialValue: number;
  seed: number;
};

const store = MvccStore.attach(sab, keyCount, poolCapacity);

// 单写者 → 本地镜像当前值，无需从共享内存回读。
const values = new Float64Array(keyCount).fill(initialValue);

// 确定性 LCG，便于复现。
let rngState = seed >>> 0;
const rand = (): number => {
  rngState = (rngState * 1664525 + 1013904223) >>> 0;
  return rngState / 2 ** 32;
};

for (let i = 0; i < commits; i++) {
  const a = (rand() * keyCount) | 0;
  let b = (rand() * keyCount) | 0;
  if (b === a) b = (b + 1) % keyCount;
  const amount = 1 + ((rand() * 100) | 0);
  values[a] -= amount;
  values[b] += amount;
  store.commit([
    [a, values[a]],
    [b, values[b]],
  ]);
}

store.markWriterDone();
parentPort!.postMessage({ commits });
