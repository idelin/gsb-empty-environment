import type { MeasurementStore } from "./measurement.ts";
import type { ConstraintSet } from "./constraint.ts";

/**
 * Evidence store with incremental revocation.
 *
 * Every judgment is cached together with a dependency snapshot (measurement
 * versions + constraint-set version). When a measurement or constraint
 * changes, affected judgments are REVOKED — never mutated — and both the
 * "issued" and "revoked" events are appended to an append-only history, so
 * past determinations remain auditable.
 */

export interface DependencySnapshot {
  readonly measurementVersions: ReadonlyMap<string, number>;
  readonly constraintVersion: number;
}

export interface EvidenceEntry<T> {
  readonly key: string;
  readonly value: T;
  readonly deps: DependencySnapshot;
  readonly issuedAtEpoch: number;
  revokedAtEpoch: number | null;
  revokeReason: string | null;
}

export interface HistoryEvent {
  readonly epoch: number;
  readonly key: string;
  readonly event: "issued" | "revoked";
  readonly reason: string;
}

export class EvidenceStore<T> {
  private readonly measurements: MeasurementStore;
  private readonly constraints: ConstraintSet;
  private entries = new Map<string, EvidenceEntry<T>[]>();
  private readonly historyLog: HistoryEvent[] = [];
  private epochCounter = 0;

  constructor(measurements: MeasurementStore, constraints: ConstraintSet) {
    this.measurements = measurements;
    this.constraints = constraints;
    measurements.onChange((change) => {
      this.epochCounter += 1;
      this.revokeWhere(
        (deps) => deps.measurementVersions.get(change.id) !== change.version,
        `measurement ${change.id} changed to version ${change.version}`,
      );
    });
  }

  private knownConstraintVersion = 0;

  private revokeWhere(
    stale: (deps: DependencySnapshot) => boolean,
    reason: string,
  ): void {
    for (const list of this.entries.values()) {
      for (const entry of list) {
        if (entry.revokedAtEpoch === null && stale(entry.deps)) {
          entry.revokedAtEpoch = this.epochCounter;
          entry.revokeReason = reason;
          this.historyLog.push({
            epoch: this.epochCounter,
            key: entry.key,
            event: "revoked",
            reason,
          });
        }
      }
    }
  }

  /** Call when the constraint set may have changed. */
  syncConstraints(): void {
    const v = this.constraints.version;
    if (v !== this.knownConstraintVersion) {
      this.epochCounter += 1;
      this.revokeWhere(
        (deps) => deps.constraintVersion !== v,
        `constraint set changed to version ${v}`,
      );
      this.knownConstraintVersion = v;
    }
  }

  private snapshot(): DependencySnapshot {
    this.knownConstraintVersion = this.constraints.version;
    return {
      measurementVersions: this.measurements.versions(),
      constraintVersion: this.constraints.version,
    };
  }

  /** Latest non-revoked entry for a key, if any. */
  active(key: string): EvidenceEntry<T> | null {
    this.syncConstraints();
    const list = this.entries.get(key);
    if (!list) return null;
    for (let i = list.length - 1; i >= 0; i--) {
      if (list[i].revokedAtEpoch === null) return list[i];
    }
    return null;
  }

  /**
   * Return the active cached judgment, or compute, store and return a new
   * one. The compute callback runs only when no active evidence exists.
   */
  getOrCompute(key: string, compute: () => T): EvidenceEntry<T> {
    this.syncConstraints();
    const existing = this.active(key);
    if (existing) return existing;
    this.epochCounter += 1;
    const entry: EvidenceEntry<T> = {
      key,
      value: compute(),
      deps: this.snapshot(),
      issuedAtEpoch: this.epochCounter,
      revokedAtEpoch: null,
      revokeReason: null,
    };
    const list = this.entries.get(key) ?? [];
    list.push(entry);
    this.entries.set(key, list);
    this.historyLog.push({
      epoch: this.epochCounter,
      key,
      event: "issued",
      reason: "computed",
    });
    return entry;
  }

  /** All entries (active and revoked) for a key, oldest first. */
  historyOf(key: string): readonly EvidenceEntry<T>[] {
    return this.entries.get(key) ?? [];
  }

  /** Append-only audit log of every issued/revoked event. */
  history(): readonly HistoryEvent[] {
    return this.historyLog;
  }
}
