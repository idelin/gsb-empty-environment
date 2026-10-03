import type { Cluster } from './cluster.ts';
import type { NodeId, Value } from './types.ts';

/**
 * Test-side safety oracle. `check` is meant to run after every tick; any
 * returned string is a protocol safety violation.
 *
 * Checked invariants:
 *  1. State-machine safety: two nodes never commit different values at the
 *     same slot (tracked against the first committed value ever observed).
 *  2. No accept-after-commit divergence: once a slot is committed anywhere,
 *     no node may subsequently accept a different value at that slot.
 *  3. Hole-free commit: every committed slot is present in the acceptor log
 *     with the committed value (the committed prefix has no gaps).
 *  4. Election safety: at most one leader per ballot.
 */
export class SafetyOracle {
  private readonly committedValues = new Map<number, Value>();
  private readonly committedAt = new Map<number, number>();
  private readonly leadersByBallot = new Map<string, NodeId>();

  check(cluster: Cluster): string[] {
    const violations: string[] = [];
    for (const node of cluster.nodes) {
      for (let slot = 0; slot < node.applied.length; slot++) {
        const entry = node.entryAt(slot);
        if (entry === undefined) {
          violations.push(`${node.id}: committed slot ${slot} missing from acceptor log`);
        } else if (entry.value !== node.applied[slot]) {
          violations.push(`${node.id}: committed slot ${slot} value diverged from log`);
        }
      }
      node.applied.forEach((value, slot) => {
        const prev = this.committedValues.get(slot);
        if (prev === undefined) {
          this.committedValues.set(slot, value);
          this.committedAt.set(slot, cluster.now);
        } else if (prev !== value) {
          violations.push(
            `SAFETY: slot ${slot} committed as "${prev}" and later as "${value}" on ${node.id}`,
          );
        }
      });
      for (const [slot, entry] of node.acceptedEntries()) {
        const committedValue = this.committedValues.get(slot);
        const committedAt = this.committedAt.get(slot);
        if (
          committedValue !== undefined &&
          committedAt !== undefined &&
          entry.value !== committedValue &&
          entry.acceptedAt > committedAt
        ) {
          violations.push(
            `SAFETY: ${node.id} accepted "${entry.value}" at slot ${slot} after it was committed as "${committedValue}"`,
          );
        }
      }
      if (node.isLeader && node.leaderBallot !== null) {
        const key = `${node.leaderBallot.round}@${node.leaderBallot.node}`;
        const holder = this.leadersByBallot.get(key);
        if (holder !== undefined && holder !== node.id) {
          violations.push(`SAFETY: ballot ${key} has two leaders: ${holder} and ${node.id}`);
        }
        this.leadersByBallot.set(key, node.id);
      }
    }
    return violations;
  }

  committedLog(): Value[] {
    const out: Value[] = [];
    for (const [slot, value] of [...this.committedValues.entries()].sort((a, b) => a[0] - b[0])) {
      out[slot] = value;
    }
    return out;
  }
}
