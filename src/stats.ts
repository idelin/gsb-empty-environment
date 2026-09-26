export interface StatsSnapshot {
  macs: number;
  tilesComputed: number;
  spillCount: number;
  spillBytes: number;
  loadCount: number;
  cacheBytes: number;
  scratchBytes: number;
  peakBytes: number;
}

export class Stats {
  macs = 0;
  tilesComputed = 0;
  spillCount = 0;
  spillBytes = 0;
  loadCount = 0;
  cacheBytes = 0;
  scratchBytes = 0;
  peakBytes = 0;

  addMacs(n: number): void {
    this.macs += n;
  }

  trackPeak(): void {
    const now = this.cacheBytes + this.scratchBytes;
    if (now > this.peakBytes) this.peakBytes = now;
  }

  snapshot(): StatsSnapshot {
    return {
      macs: this.macs,
      tilesComputed: this.tilesComputed,
      spillCount: this.spillCount,
      spillBytes: this.spillBytes,
      loadCount: this.loadCount,
      cacheBytes: this.cacheBytes,
      scratchBytes: this.scratchBytes,
      peakBytes: this.peakBytes,
    };
  }
}
