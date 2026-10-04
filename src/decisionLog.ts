import { appendFileSync, existsSync, mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Decision } from './types.ts';

/**
 * Durable, append-only log of migration decisions.
 * A local copy may only be reclaimed after its decision is persisted here.
 */
export class DecisionLog {
  private readonly file: string;
  private readonly decisions: Decision[] = [];

  constructor(dir: string) {
    mkdirSync(dir, { recursive: true });
    this.file = join(dir, 'decisions.jsonl');
    if (existsSync(this.file)) {
      for (const line of readFileSync(this.file, 'utf8').split('\n')) {
        if (line.trim()) this.decisions.push(JSON.parse(line) as Decision);
      }
    }
  }

  /** Synchronous append: durable before the call returns. */
  persist(decision: Decision): void {
    appendFileSync(this.file, JSON.stringify(decision) + '\n');
    this.decisions.push(decision);
  }

  has(objectId: string, version: number, digest: string): boolean {
    return this.decisions.some(
      (d) => d.objectId === objectId && d.version === version && d.digest === digest,
    );
  }

  get size(): number {
    return this.decisions.length;
  }
}
