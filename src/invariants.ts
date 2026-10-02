import { compareBallot, ballotKey, valueEquals, valueToString } from './types.ts';
import type { Ballot, LogEntry, NodeId } from './types.ts';

export type ViolationKind =
  | 'BALLOT_REGRESSION'
  | 'COMMITTED_OVERWRITE'
  | 'COMMITTED_CONFLICT'
  | 'LOG_HOLE'
  | 'LEASE_CONFLICT'
  | 'HASH_INTEGRITY'
  | 'LEAK';

export interface Violation {
  kind: ViolationKind;
  node: NodeId | 'cluster';
  detail: string;
}

interface LeaseHolder {
  readonly id: NodeId;
  leaseValid(): boolean;
  pendingOperations(): number;
  readonly committed: ReadonlyMap<number, LogEntry>;
  readonly commitIndex: number;
}

/**
 * Invariant assertion instrument. Hooks are wired into the consensus
 * nodes; every safety-critical state transition is checked eagerly and
 * recorded. `assertClean` / `assertNoLeaks` are strict: any recorded
 * violation or leftover pending operation fails the test run.
 */
export class InvariantMonitor {
  readonly violations: Violation[] = [];
  private nodes: LeaseHolder[] = [];
  private globalCommitted = new Map<number, LogEntry>();

  bind(nodes: LeaseHolder[]): void {
    this.nodes = nodes;
  }

  private fail(kind: ViolationKind, node: NodeId | 'cluster', detail: string): void {
    this.violations.push({ kind, node, detail });
  }

  /** Acceptor promised ballot must never decrease. */
  onPromise(node: NodeId, oldBallot: Ballot, newBallot: Ballot): void {
    if (compareBallot(newBallot, oldBallot) < 0) {
      this.fail(
        'BALLOT_REGRESSION',
        node,
        `promised ballot regressed ${ballotKey(oldBallot)} -> ${ballotKey(newBallot)}`,
      );
    }
  }

  /** A log slot may only be overwritten by a higher ballot, and never once committed. */
  onOverwrite(node: NodeId, slot: number, oldEntry: LogEntry, newEntry: LogEntry, wasCommitted: boolean): void {
    if (compareBallot(newEntry.ballot, oldEntry.ballot) < 0) {
      this.fail(
        'BALLOT_REGRESSION',
        node,
        `slot ${slot} overwritten by lower ballot ${ballotKey(newEntry.ballot)} < ${ballotKey(oldEntry.ballot)}`,
      );
    }
    if (wasCommitted && !valueEquals(oldEntry.value, newEntry.value)) {
      this.fail(
        'COMMITTED_OVERWRITE',
        node,
        `committed slot ${slot} value changed ${valueToString(oldEntry.value)} -> ${valueToString(newEntry.value)}`,
      );
    }
  }

  /** Cluster-wide: a committed (slot -> value) decision is singular and immutable. */
  onCommit(node: NodeId, slot: number, entry: LogEntry): void {
    const decided = this.globalCommitted.get(slot);
    if (decided && !valueEquals(decided.value, entry.value)) {
      this.fail(
        'COMMITTED_CONFLICT',
        node,
        `slot ${slot} committed as ${valueToString(entry.value)} but already decided ${valueToString(decided.value)}`,
      );
      return;
    }
    if (!decided) this.globalCommitted.set(slot, entry);
  }

  /** Two different values can never both be committed at the same slot. */
  onCommittedConflict(node: NodeId, slot: number, committedEntry: LogEntry, incoming: LogEntry): void {
    this.fail(
      'COMMITTED_CONFLICT',
      node,
      `slot ${slot}: incoming ${valueToString(incoming.value)} conflicts with committed ${valueToString(committedEntry.value)}`,
    );
  }

  /** Entry hash must match its content (corruption / tamper detection). */
  onIntegrity(node: NodeId, entry: LogEntry): void {
    this.fail('HASH_INTEGRITY', node, `slot ${entry.slot} hash ${entry.hash} does not match content`);
  }

  /** At most one node may hold a valid leader lease at a quiescent point. */
  private checkLeaseExclusivity(): void {
    const holders = this.nodes.filter((n) => n.leaseValid());
    if (holders.length > 1) {
      this.fail(
        'LEASE_CONFLICT',
        'cluster',
        `${holders.length} simultaneous lease holders: ${holders.map((h) => h.id).join(', ')}`,
      );
    }
  }

  /** Cross-node invariants over the current cluster state. */
  checkCluster(nodes: LeaseHolder[] = this.nodes): void {
    const decided = new Map<number, LogEntry>();
    for (const node of nodes) {
      for (let slot = 1; slot <= node.commitIndex; slot++) {
        if (!node.committed.has(slot)) {
          this.fail('LOG_HOLE', node.id, `commitIndex ${node.commitIndex} but slot ${slot} is not committed`);
        }
      }
      for (const [slot, entry] of node.committed) {
        const prev = decided.get(slot);
        if (prev && !valueEquals(prev.value, entry.value)) {
          this.fail(
            'COMMITTED_CONFLICT',
            'cluster',
            `slot ${slot} committed as both ${valueToString(prev.value)} and ${valueToString(entry.value)}`,
          );
        } else if (!prev) {
          decided.set(slot, entry);
        }
      }
    }
    this.checkLeaseExclusivity();
  }

  /** Strict leak check: no dangling collectors / pending operations. */
  assertNoLeaks(nodes: LeaseHolder[] = this.nodes): void {
    for (const node of nodes) {
      const pending = node.pendingOperations();
      if (pending !== 0) {
        this.fail('LEAK', node.id, `${pending} pending operation(s) not settled`);
      }
    }
    if (this.violations.length > 0) {
      throw new Error(`invariant violations:\n${this.format()}`);
    }
  }

  format(): string {
    return this.violations.map((v) => `[${v.kind}] ${v.node}: ${v.detail}`).join('\n');
  }

  assertClean(): void {
    if (this.violations.length > 0) {
      throw new Error(`invariant violations:\n${this.format()}`);
    }
  }
}
