import type { Schema } from "./schema.ts";
import { assertSupportedSchema } from "./schema.ts";
import { schemaDigest } from "./digest.ts";

export type MigrationFn = (doc: Record<string, unknown>) => Record<string, unknown>;

export interface Migration {
  from: string;
  to: string;
  migrate: MigrationFn;
}

interface SchemaEntry {
  version: string;
  schema: Schema;
  digest: string;
}

/** Thrown when the same version is registered with a different schema digest. */
export class SchemaConflictError extends Error {
  readonly version: string;
  readonly existingDigest: string;
  readonly incomingDigest: string;
  constructor(version: string, existingDigest: string, incomingDigest: string) {
    super(
      `schema conflict for version "${version}": ` +
        `existing digest ${existingDigest} != incoming digest ${incomingDigest}`,
    );
    this.name = "SchemaConflictError";
    this.version = version;
    this.existingDigest = existingDigest;
    this.incomingDigest = incomingDigest;
  }
}

export class Registry {
  private readonly schemas = new Map<string, SchemaEntry>();
  private readonly migrations: Migration[] = [];

  /**
   * Register a schema for a version. Re-registering the identical schema
   * (same digest) is a no-op; a different digest for the same version is a
   * conflict and throws SchemaConflictError.
   */
  registerSchema(version: string, schema: Schema): string {
    assertSupportedSchema(schema);
    const digest = schemaDigest(schema);
    const existing = this.schemas.get(version);
    if (existing) {
      if (existing.digest !== digest) {
        throw new SchemaConflictError(version, existing.digest, digest);
      }
      return digest;
    }
    this.schemas.set(version, { version, schema, digest });
    return digest;
  }

  /** Register one explicit migration step. Chains are never inferred. */
  registerMigration(from: string, to: string, migrate: MigrationFn): void {
    if (!this.schemas.has(from)) {
      throw new Error(`cannot register migration: unknown source version "${from}"`);
    }
    if (!this.schemas.has(to)) {
      throw new Error(`cannot register migration: unknown target version "${to}"`);
    }
    if (this.migrations.some((m) => m.from === from && m.to === to)) {
      throw new Error(`migration ${from} -> ${to} is already registered`);
    }
    this.migrations.push({ from, to, migrate });
  }

  hasVersion(version: string): boolean {
    return this.schemas.has(version);
  }

  versions(): string[] {
    return [...this.schemas.keys()];
  }

  getSchema(version: string): Schema | undefined {
    return this.schemas.get(version)?.schema;
  }

  getDigest(version: string): string | undefined {
    return this.schemas.get(version)?.digest;
  }

  /**
   * Resolve an explicit migration chain from `from` to `to` via BFS.
   * Returns null when no chain exists. Only registered steps are used.
   */
  findMigrationPath(from: string, to: string): Migration[] | null {
    if (from === to) return [];
    const queue: Array<{ version: string; path: Migration[] }> = [
      { version: from, path: [] },
    ];
    const visited = new Set([from]);
    while (queue.length > 0) {
      const current = queue.shift()!;
      for (const m of this.migrations) {
        if (m.from !== current.version || visited.has(m.to)) continue;
        const next = [...current.path, m];
        if (m.to === to) return next;
        visited.add(m.to);
        queue.push({ version: m.to, path: next });
      }
    }
    return null;
  }
}
