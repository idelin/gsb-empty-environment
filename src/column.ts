import type { Value } from './types.ts';

/**
 * Physical layout of one column inside a batch:
 *  - `values`:   dense value buffer, one slot per row (contents of null slots are undefined)
 *  - `validity`: bitmap, 1 bit per row, bit set = value present, bit clear = null
 */
export class ColumnVector {
  readonly name: string;
  readonly values: ReadonlyArray<Value>;
  readonly validity: Uint8Array;

  private constructor(name: string, values: ReadonlyArray<Value>, validity: Uint8Array) {
    this.name = name;
    this.values = values;
    this.validity = validity;
  }

  static fromValues(name: string, values: ReadonlyArray<Value>): ColumnVector {
    const validity = new Uint8Array(Math.ceil(values.length / 8));
    for (let i = 0; i < values.length; i++) {
      if (values[i] !== null) validity[i >> 3] |= 1 << (i & 7);
    }
    return new ColumnVector(name, values, validity);
  }

  get length(): number {
    return this.values.length;
  }

  isNull(i: number): boolean {
    return (this.validity[i >> 3] & (1 << (i & 7))) === 0;
  }

  value(i: number): Value {
    return this.isNull(i) ? null : this.values[i];
  }

  validityBits(): string {
    let out = '';
    for (let i = 0; i < this.values.length; i++) out += this.isNull(i) ? '0' : '1';
    return out;
  }
}

/**
 * A view onto a column of a live batch. The liveness probe is consulted on
 * every access, so reading through a view after its batch was released throws.
 */
export class ColumnView {
  private readonly assertAlive: () => void;
  readonly vector: ColumnVector;

  constructor(assertAlive: () => void, vector: ColumnVector) {
    this.assertAlive = assertAlive;
    this.vector = vector;
  }

  get length(): number {
    return this.vector.length;
  }

  value(i: number): Value {
    this.assertAlive();
    return this.vector.value(i);
  }
}
