/**
 * Execution models for the expression `A * B + 10`:
 *
 * 1. VectorizedExecutor — processes a whole chunk (1024 values) per call via
 *    the fused kernel over compact contiguous memory. This is the columnar
 *    batch model (Arrow / Velox / DuckDB style).
 *
 * 2. VolcanoExecutor — classic iterator model: one `next()` call per row per
 *    operator, tuples pulled through a scan -> mul -> add pipeline.
 *
 * 3. InterpretedRowExecutor — row-at-a-time expression interpreter: the
 *    expression is compiled to a stack-machine program and re-interpreted for
 *    every single row (Postgres/MySQL-style expression evaluation).
 */

import { Vector } from './vector.ts';
import { mulAddScalar } from './kernels.ts';

export class VectorizedExecutor {
  private readonly a: Vector;
  private readonly b: Vector;
  private readonly scalar: number;
  private readonly out: Vector;

  constructor(a: Vector, b: Vector, scalar: number) {
    this.a = a;
    this.b = b;
    this.scalar = scalar;
    this.out = new Vector(a.length);
  }

  /** Evaluate the whole chunk in one compact-memory pass. */
  run(): Vector {
    mulAddScalar(this.a, this.b, this.scalar, this.out);
    return this.out;
  }
}

/** Volcano pull model: one virtual next() per row per operator. */
export class VolcanoExecutor {
  private readonly a: Vector;
  private readonly b: Vector;
  private readonly scalar: number;

  constructor(a: Vector, b: Vector, scalar: number) {
    this.a = a;
    this.b = b;
    this.scalar = scalar;
  }

  run(): Int32Array {
    const out = new Int32Array(this.a.length);
    const addOp = new AddScalarOperator(
      new MulOperator(new ScanOperator(this.a), new ScanOperator(this.b)),
      this.scalar,
    );
    // Pull tuples until the pipeline signals EOF with null.
    let i = 0;
    let value: number | null;
    while ((value = addOp.next()) !== null) {
      out[i++] = value;
    }
    return out;
  }
}

/** Volcano protocol: next() returns the next tuple value, or null at EOF. */
class ScanOperator {
  private readonly vec: Vector;
  private pos = 0;
  constructor(vec: Vector) {
    this.vec = vec;
  }
  next(): number | null {
    return this.pos < this.vec.length ? this.vec.data[this.pos++] : null;
  }
}

class MulOperator {
  private readonly left: ScanOperator;
  private readonly right: ScanOperator;
  constructor(left: ScanOperator, right: ScanOperator) {
    this.left = left;
    this.right = right;
  }
  next(): number | null {
    const l = this.left.next();
    const r = this.right.next();
    return l === null || r === null ? null : Math.imul(l, r);
  }
}

class AddScalarOperator {
  private readonly child: MulOperator;
  private readonly scalar: number;
  constructor(child: MulOperator, scalar: number) {
    this.child = child;
    this.scalar = scalar;
  }
  next(): number | null {
    const v = this.child.next();
    return v === null ? null : v + this.scalar;
  }
}

/** Stack-machine opcodes for interpreted per-row expression evaluation. */
const Op = {
  LoadColumn: 0,
  LoadConst: 1,
  Mul: 2,
  Add: 3,
} as const;
type Op = (typeof Op)[keyof typeof Op];

interface Instruction {
  op: Op;
  operand: number;
}

/**
 * Row-at-a-time expression interpreter. The program for `A * B + 10` is
 * re-executed for every row, with opcode dispatch per instruction per row —
 * the execution model of classic non-vectorized query engines.
 */
export class InterpretedRowExecutor {
  private readonly columns: Int32Array[];
  private readonly program: Instruction[];

  constructor(a: Vector, b: Vector, scalar: number) {
    this.columns = [a.data, b.data];
    // A * B + 10  ==>  load A, load B, mul, push 10, add
    this.program = [
      { op: Op.LoadColumn, operand: 0 },
      { op: Op.LoadColumn, operand: 1 },
      { op: Op.Mul, operand: 0 },
      { op: Op.LoadConst, operand: scalar },
      { op: Op.Add, operand: 0 },
    ];
  }

  run(): Int32Array {
    const n = this.columns[0].length;
    const out = new Int32Array(n);
    const program = this.program;
    const columns = this.columns;
    const stack = new Int32Array(8);
    for (let row = 0; row < n; row++) {
      let sp = 0;
      for (let pc = 0; pc < program.length; pc++) {
        const instr = program[pc];
        switch (instr.op) {
          case Op.LoadColumn:
            stack[sp++] = columns[instr.operand][row];
            break;
          case Op.LoadConst:
            stack[sp++] = instr.operand;
            break;
          case Op.Mul:
            stack[sp - 2] = Math.imul(stack[sp - 2], stack[sp - 1]);
            sp--;
            break;
          case Op.Add:
            stack[sp - 2] = stack[sp - 2] + stack[sp - 1];
            sp--;
            break;
        }
      }
      out[row] = stack[0];
    }
    return out;
  }
}
