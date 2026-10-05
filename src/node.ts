export class SimNode {
  generation = 1;
  alive = true;
  pausedUntil = -Infinity;
  crashedAt: number | undefined;
  readonly id: string;

  constructor(id: string) {
    this.id = id;
  }

  isPaused(now: number): boolean {
    return now < this.pausedUntil;
  }
}
