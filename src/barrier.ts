export class Barrier {
  private arrived = 0;
  private waiters: Array<() => void> = [];
  private readonly parties: number;

  constructor(parties: number) {
    if (!Number.isInteger(parties) || parties < 1) {
      throw new RangeError(`parties must be a positive integer, got ${parties}`);
    }
    this.parties = parties;
  }

  async wait(): Promise<void> {
    if (this.parties === 1) {
      return;
    }
    this.arrived += 1;
    if (this.arrived === this.parties) {
      this.arrived = 0;
      const waiters = this.waiters.splice(0);
      for (const release of waiters) {
        release();
      }
      return;
    }
    return new Promise<void>((resolve) => {
      this.waiters.push(resolve);
    });
  }
}

export function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
