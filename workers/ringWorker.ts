import { workerData } from 'node:worker_threads';
import { MpmcRingQueue } from '../src/lockfree/mpmcRingQueue.ts';

interface WorkerData {
  role: 'producer' | 'consumer';
  buffer: SharedArrayBuffer;
  statsBuffer: SharedArrayBuffer;
  start: number;
  count: number;
  numProducers: number;
}

const data = workerData as WorkerData;
const queue = MpmcRingQueue.attach(data.buffer);
const stats = new Int32Array(data.statsBuffer);
// stats 布局：[0]=完成的生产者数, [1]=已消费总数, [2..]=每个元素的命中次数

if (data.role === 'producer') {
  for (let i = 0; i < data.count; i++) {
    const value = data.start + i;
    while (!queue.enqueue(value)) {
      // 队列满：自旋重试（无锁算法的正常竞争路径）
    }
  }
  Atomics.add(stats, 0, 1);
} else {
  for (;;) {
    const value = queue.dequeue();
    if (value === MpmcRingQueue.EMPTY) {
      // 生产者全部完成且队列已空 => 所有元素均已被消费
      if (Atomics.load(stats, 0) === data.numProducers) break;
      continue;
    }
    Atomics.add(stats, 2 + value, 1);
    Atomics.add(stats, 1, 1);
  }
}
