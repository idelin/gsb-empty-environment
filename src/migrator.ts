import { AuditLog } from './audit.ts';
import { SchemaRegistry, UnknownMigrationChainError } from './schema.ts';
import type { Store } from './store.ts';
import type { State } from './types.ts';
import { deepClone, nowIso } from './util.ts';

export class MigrationFailedError extends Error {
  readonly fromVersion: string;
  readonly toVersion: string;
  override readonly cause: unknown;

  constructor(fromVersion: string, toVersion: string, cause: unknown) {
    super(
      `migration from schema "${fromVersion}" to "${toVersion}" failed: ${
        cause instanceof Error ? cause.message : String(cause)
      }`,
    );
    this.name = 'MigrationFailedError';
    this.fromVersion = fromVersion;
    this.toVersion = toVersion;
    this.cause = cause;
  }
}

export interface MigrationResult {
  status: 'noop' | 'migrated';
  from: string;
  to: string;
  steps: number;
}

export class Migrator {
  #registry: SchemaRegistry;
  #audit: AuditLog;

  constructor(registry: SchemaRegistry, audit: AuditLog = new AuditLog()) {
    this.#registry = registry;
    this.#audit = audit;
  }

  get audit(): AuditLog {
    return this.#audit;
  }

  migrate(store: Store, target: string): MigrationResult {
    const from = store.schemaVersion;
    if (from === target) {
      return { status: 'noop', from, to: target, steps: 0 };
    }
    const path = this.#registry.findPath(from, target);
    if (path === null) {
      this.#audit.record('migrate-rejected', {
        fromVersion: from,
        toVersion: target,
        detail: 'unknown migration chain',
      });
      throw new UnknownMigrationChainError(from, target);
    }

    store.pauseSubscriptions();
    try {
      let working: State = deepClone(store.getState());
      for (const migration of path) {
        working = migration.up(working);
        if (
          working === null ||
          typeof working !== 'object' ||
          Array.isArray(working)
        ) {
          throw new Error(
            `migration ${migration.from} -> ${migration.to} returned invalid state`,
          );
        }
        this.#audit.record('migrate-step', {
          fromVersion: migration.from,
          toVersion: migration.to,
          renames: migration.renames ?? {},
        });
      }

      for (const migration of path) {
        if (migration.renames) store.remapBindings(migration.renames);
      }
      store.replaceState(working, target, {
        type: 'replace',
        at: nowIso(),
        reason: 'migration',
        fromVersion: from,
        toVersion: target,
        state: deepClone(working),
      });
      this.#audit.record('migrate-commit', {
        fromVersion: from,
        toVersion: target,
      });
      return { status: 'migrated', from, to: target, steps: path.length };
    } catch (error) {
      this.#audit.record('migrate-failed', {
        fromVersion: from,
        toVersion: target,
        detail: error instanceof Error ? error.message : String(error),
      });
      throw new MigrationFailedError(from, target, error);
    } finally {
      store.resumeSubscriptions();
    }
  }
}
