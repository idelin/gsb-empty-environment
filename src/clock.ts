export type TimeSource = () => number;

// A pausable clock. Active time is accumulated segment by segment; wall time
// that passes while paused is never folded in, so pause/resume cycles do not
// accumulate drift.
export class Clock {
  private accumulated = 0;
  private segmentStart: number | null = null;
  private readonly source: TimeSource;

  constructor(source: TimeSource) {
    this.source = source;
  }

  get running(): boolean {
    return this.segmentStart !== null;
  }

  start(): void {
    if (this.segmentStart === null) this.segmentStart = this.source();
  }

  pause(): void {
    if (this.segmentStart === null) return;
    this.accumulated += this.source() - this.segmentStart;
    this.segmentStart = null;
  }

  resume(): void {
    this.start();
  }

  reset(): void {
    this.accumulated = 0;
    this.segmentStart = null;
  }

  now(): number {
    return this.accumulated + (this.segmentStart === null ? 0 : this.source() - this.segmentStart);
  }
}
