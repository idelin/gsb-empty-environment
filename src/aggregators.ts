import type { AggregationType } from './types.js';

export interface MeasureState {
  add(value: number): void;
  remove(value: number): void;
  merge(other: MeasureState): void;
  value(): number | null;
}

export class SumState implements MeasureState {
  private sum = 0;
  add(value: number): void {
    this.sum += value;
  }
  remove(value: number): void {
    this.sum -= value;
  }
  merge(other: MeasureState): void {
    this.sum += (other as SumState).sum;
  }
  value(): number | null {
    return this.sum;
  }
}

export class CountState implements MeasureState {
  private count = 0;
  add(value: number): void {
    void value;
    this.count += 1;
  }
  remove(value: number): void {
    void value;
    this.count -= 1;
  }
  merge(other: MeasureState): void {
    this.count += (other as CountState).count;
  }
  value(): number | null {
    return this.count;
  }
}

export class AvgState implements MeasureState {
  private sum = 0;
  private count = 0;
  add(value: number): void {
    this.sum += value;
    this.count += 1;
  }
  remove(value: number): void {
    this.sum -= value;
    this.count -= 1;
  }
  merge(other: MeasureState): void {
    const source = other as AvgState;
    this.sum += source.sum;
    this.count += source.count;
  }
  value(): number | null {
    if (this.count === 0) {
      return null;
    }
    return this.sum / this.count;
  }
}

export class MinMaxState implements MeasureState {
  private readonly mode: string;
  private readonly values = new Map<number, number>();
  constructor(mode: string) {
    this.mode = mode;
  }
  add(value: number): void {
    this.values.set(value, (this.values.get(value) ?? 0) + 1);
  }
  remove(value: number): void {
    const current = this.values.get(value) ?? 0;
    if (current <= 1) {
      this.values.delete(value);
    } else {
      this.values.set(value, current - 1);
    }
  }
  merge(other: MeasureState): void {
    const source = (other as MinMaxState).values;
    source.forEach((count, v) => {
      this.values.set(v, (this.values.get(v) ?? 0) + count);
    });
  }
  value(): number | null {
    let best: number | null = null;
    this.values.forEach((count, v) => {
      if (count <= 0) {
        return;
      }
      if (best === null) {
        best = v;
        return;
      }
      if (this.mode === 'min' && v < best) {
        best = v;
      }
      if (this.mode === 'max' && v > best) {
        best = v;
      }
    });
    return best;
  }
}

export function createMeasureState(aggregation: AggregationType): MeasureState {
  switch (aggregation) {
    case 'sum':
      return new SumState();
    case 'count':
      return new CountState();
    case 'avg':
      return new AvgState();
    case 'min':
      return new MinMaxState('min');
    case 'max':
      return new MinMaxState('max');
    default:
      throw new Error('Unknown aggregation: ' + String(aggregation));
  }
}
