/**
 * Pipeline assembly: ScanSource -> 10 vectorized operators -> SumSink.
 */

import { ChunkPool } from './pool.ts';
import { MemoryMonitor } from './monitor.ts';
import {
  AbsOperator,
  AddConstantOperator,
  ClampOperator,
  FilterOperator,
  ModuloOperator,
  MultiplyOperator,
  ReplicateOperator,
  RunningSumOperator,
  ScaleOffsetOperator,
  ScanSource,
  SqrtOperator,
  SumSink,
} from './operators.ts';
import type { Operator } from './operators.ts';

export interface Pipeline {
  readonly pool: ChunkPool;
  readonly sink: SumSink;
}

export function buildPipeline(
  totalRows: number,
  options?: { capacity?: number; chunkSize?: number; monitor?: MemoryMonitor },
): Pipeline {
  const capacity = options?.capacity ?? 16;
  const chunkSize = options?.chunkSize ?? 4096;
  const pool = new ChunkPool(capacity, chunkSize);

  let op: Operator = new ScanSource(pool, totalRows);
  op = new FilterOperator(op, 3);          //  1. selection
  op = new AddConstantOperator(op, 7.5);   //  2. projection
  op = new MultiplyOperator(op, 1.25);     //  3. projection
  op = new SqrtOperator(op);               //  4. math
  op = new ClampOperator(op, 0, 500);      //  5. clamp
  op = new ModuloOperator(op, 97);         //  6. modulo
  op = new AbsOperator(op);                //  7. abs
  op = new ScaleOffsetOperator(op, 2, -3); //  8. fused scale/offset
  op = new RunningSumOperator(op);         //  9. stateful window
  op = new ReplicateOperator(op, pool);    // 10. fan-out (pool acquire/release)

  const sink = new SumSink(op, pool, options?.monitor);
  return { pool, sink };
}
