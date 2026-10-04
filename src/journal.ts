// Durable, append-only migration decision log. A RECLAIM_LOCAL decision must
// be persisted here before the local copy may be deleted.
export interface Decision {
  kind: 'RECLAIM_LOCAL';
  objectId: string;
  version: number;
  digest: string;
  seq: number;
  decidedAt: number;
}

export class Journal {
  private records: Decision[] = [];
  private seq = 0;
  // Fault injection: the next flush fails (crash before fsync).
  failNextFlush = false;

  append(objectId: string, version: number, digest: string, now: number): Decision | null {
    if (this.failNextFlush) {
      this.failNextFlush = false;
      return null; // flush failed: nothing was persisted
    }
    const decision: Decision = {
      kind: 'RECLAIM_LOCAL',
      objectId,
      version,
      digest,
      seq: ++this.seq,
      decidedAt: now,
    };
    this.records.push(decision);
    return decision;
  }

  find(objectId: string, version: number, digest: string): Decision | undefined {
    return this.records.find(
      (r) => r.objectId === objectId && r.version === version && r.digest === digest,
    );
  }

  size(): number {
    return this.records.length;
  }
}
