import type { AliasOp, Snapshot, Target } from './types.ts';
import { aliasKey, objectKey, targetEquals } from './types.ts';

export interface CommitRecord {
  epoch: number;
  changed: ReadonlySet<string>;
}

export class AliasStore {
  private aliases = new Map<string, Target>();
  private objects = new Map<string, Map<number, unknown>>();
  private commitLog: CommitRecord[] = [];
  epoch = 0;

  /**
   * Applies a batch of alias operations atomically: either every op is
   * applied or none are. Multiple aliases in (and across) batches may share
   * the same target. Cycles are permitted at write time and reported at
   * resolution time.
   */
  commitAliasBatch(ops: AliasOp[]): number {
    const seen = new Set<string>();
    for (const op of ops) {
      if (typeof op.alias !== 'string' || op.alias.length === 0) {
        throw new Error('invalid alias name in batch');
      }
      if (seen.has(op.alias)) {
        throw new Error(`duplicate alias "${op.alias}" in batch`);
      }
      seen.add(op.alias);
      if (op.type === 'set') validateTarget(op.target);
    }

    const next = new Map(this.aliases);
    for (const op of ops) {
      if (op.type === 'set') next.set(op.alias, op.target);
      else next.delete(op.alias);
    }

    const changed = new Set<string>();
    for (const name of seen) {
      if (!targetEquals(this.aliases.get(name), next.get(name))) {
        changed.add(aliasKey(name));
      }
    }
    this.aliases = next;
    return this.commit(changed);
  }

  putObject(name: string, version: number, data: unknown): number {
    if (!Number.isInteger(version) || version < 1) {
      throw new Error(`invalid object version: ${version}`);
    }
    let versions = this.objects.get(name);
    if (versions === undefined) {
      versions = new Map();
      this.objects.set(name, versions);
    }
    versions.set(version, data);
    return this.commit(new Set([objectKey(name, version)]));
  }

  deleteObject(name: string, version: number): number {
    const versions = this.objects.get(name);
    const existed = versions?.delete(version) ?? false;
    if (versions !== undefined && versions.size === 0) {
      this.objects.delete(name);
    }
    return this.commit(existed ? new Set([objectKey(name, version)]) : new Set());
  }

  private commit(changed: Set<string>): number {
    this.epoch += 1;
    this.commitLog.push({ epoch: this.epoch, changed });
    return this.epoch;
  }

  /** Captures an immutable, consistent view of the store at the current epoch. */
  snapshot(): Snapshot {
    return {
      epoch: this.epoch,
      aliases: new Map(this.aliases),
      objects: new Map(
        [...this.objects].map(([name, versions]) => [name, new Map(versions)]),
      ),
    };
  }

  /** Commits with epoch in (fromExclusive, toInclusive], in order. */
  commitsBetween(fromExclusive: number, toInclusive: number): CommitRecord[] {
    const out: CommitRecord[] = [];
    for (const record of this.commitLog) {
      if (record.epoch > fromExclusive && record.epoch <= toInclusive) {
        out.push(record);
      }
    }
    return out;
  }
}

function validateTarget(target: Target): void {
  if (target === null || typeof target !== 'object') {
    throw new Error('invalid target');
  }
  if (target.kind === 'alias') {
    if (typeof target.name !== 'string' || target.name.length === 0) {
      throw new Error('invalid alias target name');
    }
    return;
  }
  if (target.kind === 'object') {
    if (typeof target.name !== 'string' || target.name.length === 0) {
      throw new Error('invalid object target name');
    }
    if (!Number.isInteger(target.version) || target.version < 1) {
      throw new Error(`invalid object target version: ${target.version}`);
    }
    return;
  }
  throw new Error(`invalid target kind: ${(target as { kind: unknown }).kind}`);
}
