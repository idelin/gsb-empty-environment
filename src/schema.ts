import type { Migration } from './types.ts';

export class UnknownMigrationChainError extends Error {
  readonly fromVersion: string;
  readonly toVersion: string;

  constructor(fromVersion: string, toVersion: string) {
    super(`no migration chain from schema "${fromVersion}" to "${toVersion}"`);
    this.name = 'UnknownMigrationChainError';
    this.fromVersion = fromVersion;
    this.toVersion = toVersion;
  }
}

export class SchemaRegistry {
  #versions = new Set<string>();
  #migrations = new Map<string, Migration>();

  registerVersion(version: string): void {
    this.#versions.add(version);
  }

  hasVersion(version: string): boolean {
    return this.#versions.has(version);
  }

  addMigration(migration: Migration): void {
    for (const version of [migration.from, migration.to]) {
      if (!this.#versions.has(version)) {
        throw new Error(`unregistered schema version: "${version}"`);
      }
    }
    const key = `${migration.from}->${migration.to}`;
    if (this.#migrations.has(key)) {
      throw new Error(`duplicate migration: ${key}`);
    }
    this.#migrations.set(key, migration);
  }

  findPath(from: string, to: string): Migration[] | null {
    if (from === to) return [];
    const queue: Array<{ version: string; path: Migration[] }> = [
      { version: from, path: [] },
    ];
    const seen = new Set<string>([from]);
    while (queue.length > 0) {
      const current = queue.shift() as { version: string; path: Migration[] };
      for (const migration of this.#migrations.values()) {
        if (migration.from !== current.version || seen.has(migration.to)) {
          continue;
        }
        const path = [...current.path, migration];
        if (migration.to === to) return path;
        seen.add(migration.to);
        queue.push({ version: migration.to, path });
      }
    }
    return null;
  }
}
