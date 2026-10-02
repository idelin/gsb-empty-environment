const MASK_64 = 0xffffffffffffffffn;

export class SplitMix64 {
  private state: bigint;

  constructor(seed: bigint | number) {
    this.state = BigInt(seed) & MASK_64;
  }

  next(): bigint {
    this.state = (this.state + 0x9e3779b97f4a7c15n) & MASK_64;
    let z = this.state;
    z = ((z ^ (z >> 30n)) * 0xbf58476d1ce4e5b9n) & MASK_64;
    z = ((z ^ (z >> 27n)) * 0x94d049bb133111ebn) & MASK_64;
    return (z ^ (z >> 31n)) & MASK_64;
  }

  nextUint32(): number {
    return Number(this.next() >> 32n);
  }

  nextInt(boundExclusive: number): number {
    if (boundExclusive <= 0) throw new RangeError('bound must be positive');
    return Number(this.next() % BigInt(boundExclusive));
  }
}
