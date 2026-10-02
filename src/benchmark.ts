/**
 * Throughput benchmark: vectorized chunk execution vs row-at-a-time models
 * (Volcano iterator and per-row expression interpreter) for `A * B + 10`
 * over 1024-row chunks.
 *
 * Timing uses best-of-N rounds to filter out JIT/GC noise.
 */

import { CHUNK_SIZE, makeVector } from './vector.ts';
import { VectorizedExecutor, VolcanoExecutor, InterpretedRowExecutor } from './executor.ts';

export interface BenchResult {
  model: string;
  iterations: number;
  rowsPerIter: number;
  bestMs: number;
  millionRowsPerSec: number;
}

const ROUNDS = 5;

function bench(model: string, iterations: number, fn: () => unknown): BenchResult {
  // Warm up JIT.
  for (let i = 0; i < Math.min(2000, iterations); i++) fn();
  let bestMs = Infinity;
  for (let round = 0; round < ROUNDS; round++) {
    const start = performance.now();
    for (let i = 0; i < iterations; i++) fn();
    bestMs = Math.min(bestMs, performance.now() - start);
  }
  const millionRowsPerSec = (iterations * CHUNK_SIZE) / bestMs / 1000;
  return { model, iterations, rowsPerIter: CHUNK_SIZE, bestMs, millionRowsPerSec };
}

export interface BenchmarkReport {
  vectorized: BenchResult;
  volcano: BenchResult;
  interpreted: BenchResult;
  speedupVsVolcano: number;
  speedupVsInterpreted: number;
}

export function runBenchmark(iterations = 20000): BenchmarkReport {
  const a = makeVector(CHUNK_SIZE, 42);
  const b = makeVector(CHUNK_SIZE, 1337);

  const vecExec = new VectorizedExecutor(a, b, 10);
  const volcanoExec = new VolcanoExecutor(a, b, 10);
  const interpExec = new InterpretedRowExecutor(a, b, 10);

  const vectorized = bench('vectorized (chunk of 1024)', iterations, () => vecExec.run());
  const volcano = bench('volcano iterator (per-row)', iterations, () => volcanoExec.run());
  const interpreted = bench('row interpreter (per-row)', iterations, () => interpExec.run());

  return {
    vectorized,
    volcano,
    interpreted,
    speedupVsVolcano: vectorized.millionRowsPerSec / volcano.millionRowsPerSec,
    speedupVsInterpreted: vectorized.millionRowsPerSec / interpreted.millionRowsPerSec,
  };
}

function print(result: BenchResult): void {
  console.log(
    `${result.model.padEnd(32)} ${result.bestMs.toFixed(1).padStart(8)} ms  ` +
      `${result.millionRowsPerSec.toFixed(1).padStart(8)} M rows/s`,
  );
}

if (import.meta.url === `file://${process.argv[1]}`) {
  console.log(`Benchmark: A * B + 10 over ${CHUNK_SIZE}-row chunks (best of ${ROUNDS} rounds)\n`);
  const report = runBenchmark();
  print(report.vectorized);
  print(report.volcano);
  print(report.interpreted);
  console.log(`\nSpeedup vs volcano iterator : ${report.speedupVsVolcano.toFixed(2)}x`);
  console.log(`Speedup vs row interpreter  : ${report.speedupVsInterpreted.toFixed(2)}x`);
}
