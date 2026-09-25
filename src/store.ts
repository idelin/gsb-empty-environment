/**
 * 断点存储。
 * - FileCheckpointStore：写临时文件 + rename 原子替换；读到损坏文件给 CHECKPOINT_CORRUPT。
 * - InMemoryCheckpointStore：测试用。
 */
import { readFileSync, writeFileSync, renameSync, existsSync, unlinkSync } from 'node:fs';
import { dirname } from 'node:path';
import { mkdirSync } from 'node:fs';
import { MigrationError } from './errors.ts';
import type { Checkpoint } from './migrate.ts';

export interface CheckpointStore {
  load(): Checkpoint | null;
  save(checkpoint: Checkpoint): void;
  clear(): void;
}

export class InMemoryCheckpointStore implements CheckpointStore {
  private current: Checkpoint | null = null;

  load(): Checkpoint | null {
    return this.current === null ? null : (JSON.parse(JSON.stringify(this.current)) as Checkpoint);
  }

  save(checkpoint: Checkpoint): void {
    this.current = JSON.parse(JSON.stringify(checkpoint)) as Checkpoint;
  }

  clear(): void {
    this.current = null;
  }
}

function validateShape(value: unknown): asserts value is Checkpoint {
  const v = value as Partial<Checkpoint> | null;
  const reject = (message: string, location: string): never => {
    throw new MigrationError({
      code: 'CHECKPOINT_CORRUPT',
      message,
      location,
    });
  };

  if (v === null || typeof v !== 'object') reject('checkpoint is not an object', '/');
  if ((v as Partial<Checkpoint>).checkpointVersion !== 1) {
    reject('checkpointVersion must be 1', '/checkpointVersion');
  }
  const obj = v as Partial<Checkpoint>;
  if (typeof obj.graphHash !== 'string') reject('graphHash missing', '/graphHash');
  if (typeof obj.fromVersion !== 'string') reject('fromVersion missing', '/fromVersion');
  if (typeof obj.targetVersion !== 'string') reject('targetVersion missing', '/targetVersion');
  if (typeof obj.inputHash !== 'string') reject('inputHash missing', '/inputHash');
  if (typeof obj.originalEnvelope !== 'object' || obj.originalEnvelope === null) {
    reject('originalEnvelope missing', '/originalEnvelope');
  } else {
    const env = obj.originalEnvelope as { version?: unknown; data?: unknown };
    if (typeof env.version !== 'string' || typeof env.data !== 'object' || env.data === null) {
      reject('originalEnvelope malformed', '/originalEnvelope');
    }
  }
  if (!Array.isArray(obj.versionPath) || obj.versionPath.length < 2) {
    reject('versionPath malformed', '/versionPath');
  }
  if (!Array.isArray(obj.edgePath)) reject('edgePath malformed', '/edgePath');
  if (!Array.isArray(obj.executed)) reject('executed malformed', '/executed');
  for (let i = 0; i < (obj.executed as unknown[]).length; i++) {
    const entry = (obj.executed as unknown[])[i] as Record<string, unknown>;
    if (
      typeof entry.edgeId !== 'string' ||
      typeof entry.stepName !== 'string' ||
      typeof entry.version !== 'string' ||
      typeof entry.outputHash !== 'string'
    ) {
      reject(`executed[${i}] malformed`, `/executed[${i}]`);
    }
  }
  if (obj.status !== 'in-progress' && obj.status !== 'completed') {
    reject('status malformed', '/status');
  }
}

export class FileCheckpointStore implements CheckpointStore {
  private readonly path: string;

  constructor(path: string) {
    this.path = path;
  }

  load(): Checkpoint | null {
    if (!existsSync(this.path)) return null;
    let raw: string;
    try {
      raw = readFileSync(this.path, 'utf8');
    } catch (err) {
      throw new MigrationError({
        code: 'CHECKPOINT_CORRUPT',
        message: `cannot read checkpoint file: ${err instanceof Error ? err.message : String(err)}`,
        location: this.path,
      });
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch (err) {
      throw new MigrationError({
        code: 'CHECKPOINT_CORRUPT',
        message: `checkpoint file is not valid JSON: ${err instanceof Error ? err.message : String(err)}`,
        location: this.path,
      });
    }
    validateShape(parsed);
    return parsed;
  }

  save(checkpoint: Checkpoint): void {
    const tmp = `${this.path}.tmp-${process.pid}-${Date.now()}-${Math.random().toString(36).slice(2)}`;
    mkdirSync(dirname(this.path), { recursive: true });
    writeFileSync(tmp, JSON.stringify(checkpoint, null, 2), 'utf8');
    renameSync(tmp, this.path);
  }

  clear(): void {
    if (existsSync(this.path)) unlinkSync(this.path);
  }
}
