export class LoadStats {
  started = 0;
  completed = 0;
  cancelled = 0;
  failed = 0;
}

/**
 * Execution statistics: batch lifecycle counters, row throughput and
 * per-column wide-load counts (started / completed / cancelled / failed).
 */
export class Stats {
  batchesOpened = 0;
  batchesReleased = 0;
  batchesRecycled = 0;
  rowsScanned = 0;
  rowsSelected = 0;
  rowsEmitted = 0;
  readonly loads = new Map<string, LoadStats>();

  loadFor(column: string): LoadStats {
    let s = this.loads.get(column);
    if (!s) {
      s = new LoadStats();
      this.loads.set(column, s);
    }
    return s;
  }

  snapshot(): Record<string, unknown> {
    const loads: Record<string, unknown> = {};
    this.loads.forEach((v, k) => {
      loads[k] = {
        started: v.started,
        completed: v.completed,
        cancelled: v.cancelled,
        failed: v.failed,
      };
    });
    return {
      batchesOpened: this.batchesOpened,
      batchesReleased: this.batchesReleased,
      batchesRecycled: this.batchesRecycled,
      rowsScanned: this.rowsScanned,
      rowsSelected: this.rowsSelected,
      rowsEmitted: this.rowsEmitted,
      loads,
    };
  }
}
