export type HistoryOp =
  | { kind: 'read' | 'write'; txId: number; key: string }
  | { kind: 'commit'; txId: number; commitTs: number }
  | { kind: 'abort'; txId: number };

/**
 * Records the interleaved operation history and checks the spec's core
 * guarantee: the produced schedule is conflict-equivalent to the serial
 * schedule ordered by commit timestamps.
 *
 * Method: derive every conflict direction (Ti -> Tj) from the history
 * (same key, at least one write, Ti's op first). The schedule is
 * commit-order equivalent iff, for every pair of committed transactions,
 * all derived directions agree with their commit-timestamp order.
 */
export class ScheduleHistory {
  readonly ops: HistoryOp[] = [];

  record(op: HistoryOp): void {
    this.ops.push(op);
  }

  verifyCommitOrderEquivalence(): { ok: boolean; conflicts: string[] } {
    const commitTs = new Map<number, number>();
    for (const op of this.ops) {
      if (op.kind === 'commit') commitTs.set(op.txId, op.commitTs);
    }

    // Group data ops by key, preserving history order.
    const byKey = new Map<string, Array<{ txId: number; kind: 'read' | 'write' }>>();
    for (const op of this.ops) {
      if (op.kind !== 'read' && op.kind !== 'write') continue;
      const list = byKey.get(op.key) ?? [];
      list.push({ txId: op.txId, kind: op.kind });
      byKey.set(op.key, list);
    }

    // directions: "a->b" means some conflicting op of a precedes one of b.
    const directions = new Set<string>();
    for (const ops of byKey.values()) {
      for (let i = 0; i < ops.length; i++) {
        for (let j = i + 1; j < ops.length; j++) {
          const first = ops[i];
          const second = ops[j];
          if (first.txId === second.txId) continue;
          if (first.kind === 'read' && second.kind === 'read') continue;
          directions.add(`${first.txId}->${second.txId}`);
        }
      }
    }

    const conflicts: string[] = [];
    const committed = [...commitTs.keys()];
    for (let i = 0; i < committed.length; i++) {
      for (let j = i + 1; j < committed.length; j++) {
        const a = committed[i];
        const b = committed[j];
        const earlier = commitTs.get(a)! < commitTs.get(b)! ? a : b;
        const later = earlier === a ? b : a;
        // Any direction from the later-committing tx to the earlier one
        // contradicts commit-timestamp ordering.
        if (directions.has(`${later}->${earlier}`)) {
          conflicts.push(
            `tx${later} (ts=${commitTs.get(later)}) has a conflicting op before ` +
            `tx${earlier} (ts=${commitTs.get(earlier)})`,
          );
        }
      }
    }
    return { ok: conflicts.length === 0, conflicts };
  }
}
