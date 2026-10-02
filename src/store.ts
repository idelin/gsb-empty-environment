/**
 * Persistent reactive store.
 *
 * Signals registered via `store.persisted(key, initial)` (the `@persist`
 * annotation, see `persist()` below) are serialized into JSON checkpoints.
 * Signals registered via `store.signal(key, initial)` are transient and are
 * safely reset to their initial value on hydration.
 *
 * The Hydrator restores a checkpoint incrementally, entry by entry; every
 * applied write flows through the reactive graph, so all derived Effects
 * and Computeds re-align automatically.
 */

import { readFileSync, writeFileSync } from 'node:fs';
import { computed, effect, signal } from './reactive.ts';
import type { Computed, Signal } from './reactive.ts';

export const CHECKPOINT_VERSION = 1;

export interface Checkpoint {
  version: number;
  createdAt: string;
  entries: Record<string, unknown>;
}

export interface HydrationReport {
  /** Persisted keys restored from the checkpoint, in application order. */
  applied: string[];
  /** Checkpoint entries skipped (unknown or non-persisted keys). */
  skipped: string[];
  /** Transient signals reset to their initial value. */
  resetTransient: string[];
  /** Number of effect executions triggered by the hydration pass. */
  effectsReAligned: number;
}

export class Store {
  private readonly signals = new Map<string, Signal<unknown>>();
  private readonly persistedKeys = new Set<string>();
  private effectRuns = 0;

  /** Register a transient (non-persisted) signal. */
  signal<T>(key: string, initial: T): Signal<T> {
    return this.register(key, initial, false);
  }

  /** Register a persisted signal — the `@persist` annotation. */
  persisted<T>(key: string, initial: T): Signal<T> {
    return this.register(key, initial, true);
  }

  private register<T>(key: string, initial: T, persist: boolean): Signal<T> {
    if (this.signals.has(key)) {
      throw new Error(`Signal "${key}" is already registered`);
    }
    const s = signal(initial);
    this.signals.set(key, s as Signal<unknown>);
    if (persist) this.persistedKeys.add(key);
    return s;
  }

  computed<T>(fn: () => T): Computed<T> {
    return computed(fn);
  }

  effect(fn: () => void): () => void {
    return effect(() => {
      this.effectRuns += 1;
      fn();
    });
  }

  /** Serialize only the @persist-annotated signals into a JSON checkpoint. */
  checkpoint(): Checkpoint {
    const entries: Record<string, unknown> = {};
    for (const key of this.persistedKeys) {
      entries[key] = this.signals.get(key)!.peek();
    }
    return {
      version: CHECKPOINT_VERSION,
      createdAt: new Date().toISOString(),
      entries,
    };
  }

  checkpointToJSON(): string {
    return JSON.stringify(this.checkpoint(), null, 2);
  }

  saveCheckpoint(path: string): void {
    writeFileSync(path, this.checkpointToJSON(), 'utf8');
  }

  static loadCheckpoint(path: string): Checkpoint {
    const parsed = JSON.parse(readFileSync(path, 'utf8')) as Checkpoint;
    if (parsed.version !== CHECKPOINT_VERSION) {
      throw new Error(
        `Unsupported checkpoint version: ${parsed.version} (expected ${CHECKPOINT_VERSION})`,
      );
    }
    return parsed;
  }

  /**
   * Incremental hydration: apply checkpoint entries one by one. Each applied
   * value is written through the reactive graph, so dependent Computeds and
   * Effects re-align as entries stream in. Afterwards every transient signal
   * is reset to its initial value.
   */
  hydrate(checkpoint: Checkpoint): HydrationReport {
    if (checkpoint.version !== CHECKPOINT_VERSION) {
      throw new Error(
        `Unsupported checkpoint version: ${checkpoint.version} (expected ${CHECKPOINT_VERSION})`,
      );
    }
    const report: HydrationReport = {
      applied: [],
      skipped: [],
      resetTransient: [],
      effectsReAligned: 0,
    };
    const runsBefore = this.effectRuns;

    for (const [key, value] of Object.entries(checkpoint.entries)) {
      const target = this.signals.get(key);
      if (!target || !this.persistedKeys.has(key)) {
        report.skipped.push(key);
        continue;
      }
      target.set(value);
      report.applied.push(key);
    }

    for (const [key, s] of this.signals) {
      if (this.persistedKeys.has(key)) continue;
      s.reset();
      report.resetTransient.push(key);
    }

    report.effectsReAligned = this.effectRuns - runsBefore;
    return report;
  }

  hydrateFromFile(path: string): HydrationReport {
    return this.hydrate(Store.loadCheckpoint(path));
  }
}

/**
 * `@persist` annotation helper. TypeScript decorators are not evaluated by
 * Node's native type-stripping mode, so the annotation is expressed as a
 * function: `const count = persist(store, 'count', 0)`.
 */
export function persist<T>(store: Store, key: string, initial: T): Signal<T> {
  return store.persisted(key, initial);
}
