/**
 * Runnable sample: build two 1024-row int columns, evaluate `A * B + 10`
 * with the vectorized kernel, and print a few results plus the benchmark.
 */

import { CHUNK_SIZE, makeVector } from './vector.ts';
import { VectorizedExecutor } from './executor.ts';
import { runBenchmark } from './benchmark.ts';

const a = makeVector(CHUNK_SIZE, 7);
const b = makeVector(CHUNK_SIZE, 99);

console.log(`Input: two Int32 columns of ${CHUNK_SIZE} rows each`);
console.log(`A[0..4] = [${Array.from(a.data.slice(0, 5)).join(', ')}]`);
console.log(`B[0..4] = [${Array.from(b.data.slice(0, 5)).join(', ')}]`);

const result = new VectorizedExecutor(a, b, 10).run();
console.log(`\nExpression: A * B + 10`);
console.log(`out[0..4] = [${Array.from(result.data.slice(0, 5)).join(', ')}]`);
console.log(`check: ${a.data[0]} * ${b.data[0]} + 10 = ${a.data[0] * b.data[0] + 10}`);
console.log(`aligned to 64B: A=${a.isAligned} B=${b.isAligned} out=${result.isAligned}`);

console.log(`\n--- Throughput benchmark ---`);
const report = runBenchmark();
console.log(`vectorized   : ${report.vectorized.millionRowsPerSec.toFixed(1)} M rows/s`);
console.log(`volcano      : ${report.volcano.millionRowsPerSec.toFixed(1)} M rows/s`);
console.log(`interpreter  : ${report.interpreted.millionRowsPerSec.toFixed(1)} M rows/s`);
console.log(`speedup vs volcano     : ${report.speedupVsVolcano.toFixed(2)}x`);
console.log(`speedup vs interpreter : ${report.speedupVsInterpreted.toFixed(2)}x`);
