/**
 * 运行样例 + 基准：对两列 1024 整数执行 'A * B + 10'，
 * 对比向量化批量内核与逐行迭代（火山）模型的吞吐。
 */
import { CHUNK_SIZE, Int32Vector } from '../src/vector.ts';
import { makeColumn, evalMulAddScalar } from '../src/engine.ts';
import { evalRowWise } from '../src/rowwise.ts';

const n = CHUNK_SIZE; // 验收边界：1024 行
const a = new Int32Vector(n);
const b = new Int32Vector(n);
for (let i = 0; i < n; i++) {
  a.data[i] = i - 512;
  b.data[i] = (i * 7) % 100;
}
const colA = makeColumn('A', a.data);
const colB = makeColumn('B', b.data);

// --- 正确性样例 ---
const result = evalMulAddScalar(colA, colB, 10);
console.log(`表达式: A * B + 10, 行数: ${n}, Chunk 数: ${result.length}`);
console.log(`前 8 行结果: [${[...result[0].data.slice(0, 8)].join(', ')}]`);
console.log(`校验: A[0]*B[0]+10 = ${a.data[0] * b.data[0] + 10}, 引擎输出 = ${result[0].data[0]}`);

// --- 吞吐基准 ---
function bench(label: string, fn: () => unknown, iters: number): number {
  fn(); // 预热 JIT
  const t0 = process.hrtime.bigint();
  for (let i = 0; i < iters; i++) fn();
  const ms = Number(process.hrtime.bigint() - t0) / 1e6;
  const rowsPerSec = (n * iters) / (ms / 1000);
  console.log(
    `${label.padEnd(24)} ${ms.toFixed(1).padStart(10)} ms  ` +
      `${(rowsPerSec / 1e6).toFixed(1)} M rows/s`,
  );
  return ms;
}

const iters = 20000;
console.log(`\n基准: ${iters} 次迭代 x ${n} 行`);
const vecMs = bench('向量化批量内核', () => evalMulAddScalar(colA, colB, 10), iters);
const rowMs = bench('逐行迭代模型', () => evalRowWise(colA, colB, 10), iters);
console.log(`\n吞吐提升: ${(rowMs / vecMs).toFixed(2)}x`);
