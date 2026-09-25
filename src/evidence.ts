/**
 * Evidence store: the access evidence and the decision are committed
 * atomically as a single record. The file implementation writes a temp
 * file and renames it, so a decision is never observed without its
 * evidence (or vice versa).
 */
import { promises as fs } from 'node:fs';
import { join } from 'node:path';

export interface Access {
  attr: string;
  value: number;
  version: number;
}

export interface Decision {
  id: string;
  subject: string;
  scope: string;
  allow: boolean;
  accesses: Access[];
  decidedAt: number;
}

export interface EvidenceStore {
  commit(d: Decision): Promise<void>;
}

export class MemoryEvidenceStore implements EvidenceStore {
  readonly records: Decision[] = [];
  async commit(d: Decision): Promise<void> {
    this.records.push(structuredClone(d));
  }
}

export class FileEvidenceStore implements EvidenceStore {
  private readonly dir: string;
  constructor(dir: string) {
    this.dir = dir;
  }
  async commit(d: Decision): Promise<void> {
    const tmp = join(this.dir, `.${d.id}.tmp`);
    const final = join(this.dir, `${d.id}.json`);
    await fs.writeFile(tmp, JSON.stringify(d, null, 2));
    await fs.rename(tmp, final); // atomic on POSIX
  }
}
