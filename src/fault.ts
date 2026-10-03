import { SimulatedCrashError } from './errors.ts';
import type { DatabaseSystem } from './system.ts';
import type { LSN } from './types.ts';

/**
 * Fault injector for hostile-environment testing: sudden power loss, disk
 * full (ENOSPC), and crash-at-LSN triggers usable mid-recovery.
 */
export class FaultInjector {
  private readonly system: DatabaseSystem;

  constructor(system: DatabaseSystem) {
    this.system = system;
  }

  /** Sudden power loss: volatile log tail, buffer pool, locks and TT vanish. */
  powerLoss(): void {
    this.system.crash();
  }

  /** Disk full on/off. While on, every disk write fails with ENOSPC. */
  setDiskFull(full: boolean): void {
    this.system.disk.setDiskFull(full);
  }

  /**
   * Arms a one-shot crash trigger fired when `targetLSN` is appended.
   * With `flushBeforeCrash` (default) the log is forced first, so the records
   * written up to that point survive; without it the volatile tail — including
   * the triggering record — is lost, like a real sudden power loss.
   */
  crashAtLSN(targetLSN: LSN, options: { flushBeforeCrash?: boolean } = {}): void {
    const flushBeforeCrash = options.flushBeforeCrash ?? true;
    const system = this.system;
    system.setAppendHook((rec) => {
      if (rec.lsn === targetLSN) {
        system.setAppendHook(null);
        if (flushBeforeCrash) system.logManager.flushAll();
        system.crash();
        throw new SimulatedCrashError(`power loss at LSN ${targetLSN}`);
      }
    });
  }
}
