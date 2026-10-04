export class SampleWindow {
  readonly capacity: number;
  private buf: number[];
  private head = 0;
  private count = 0;

  constructor(capacity: number) {
    if (capacity < 1) throw new Error("capacity must be >= 1");
    this.capacity = capacity;
    this.buf = new Array<number>(capacity);
  }

  get size(): number {
    return this.count;
  }

  add(x: number): void {
    this.buf[this.head] = x;
    this.head = (this.head + 1) % this.capacity;
    if (this.count < this.capacity) this.count++;
  }

  get mean(): number {
    if (this.count === 0) return 0;
    let sum = 0;
    for (let i = 0; i < this.count; i++) sum += this.buf[i];
    return sum / this.count;
  }

  get stddev(): number {
    if (this.count === 0) return 0;
    const m = this.mean;
    let sum = 0;
    for (let i = 0; i < this.count; i++) {
      const d = this.buf[i] - m;
      sum += d * d;
    }
    return Math.sqrt(sum / this.count);
  }
}
