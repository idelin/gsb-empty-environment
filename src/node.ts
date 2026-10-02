/**
 * Cluster node: owns a MembershipTable and runs the periodic probe /
 * digest-compare / interval-reconcile protocol against its live peers.
 *
 * Probe protocol (one probe = one round-trip cycle against a peer):
 *   1. Exchange 4-byte CRC digests. Equal -> views are consistent, stop.
 *   2. On mismatch, exchange per-interval (hash-ring bucket) digest maps.
 *   3. Both sides ship only the member records inside the differing
 *      intervals and merge them (bidirectional pull/push).
 */

import { MembershipTable, type MemberRecord } from "./membership.ts";

export const DIGEST_BYTES = 4;

export interface ProbeOutcome {
  peerId: string;
  inSync: boolean;
  differingBuckets: number[];
  recordsPulled: number;
  recordsPushed: number;
  digestBytesExchanged: number;
  recordBytesExchanged: number;
}

export class ClusterNode {
  readonly id: string;
  readonly table: MembershipTable;
  private readonly peers = new Map<string, ClusterNode>();

  constructor(id: string, bucketCount = 16) {
    this.id = id;
    this.table = new MembershipTable(bucketCount);
  }

  connect(peer: ClusterNode): void {
    if (peer === this) return;
    this.peers.set(peer.id, peer);
    peer.peers.set(this.id, this);
  }

  disconnect(peer: ClusterNode): void {
    this.peers.delete(peer.id);
    peer.peers.delete(this.id);
  }

  peerIds(): string[] {
    return [...this.peers.keys()].sort();
  }

  /** Local bootstrap / admin update of the own membership view. */
  updateLocal(record: MemberRecord): boolean {
    return this.table.upsert(record);
  }

  /**
   * One probe cycle against a single peer. Detects divergence with a
   * 4-byte digest comparison and, on mismatch, reconciles only the
   * diverging hash-ring intervals.
   */
  probe(peer: ClusterNode): ProbeOutcome {
    if (this.peers.get(peer.id) !== peer) {
      throw new Error(`node ${this.id} is not connected to ${peer.id}`);
    }
    const outcome: ProbeOutcome = {
      peerId: peer.id,
      inSync: true,
      differingBuckets: [],
      recordsPulled: 0,
      recordsPushed: 0,
      digestBytesExchanged: DIGEST_BYTES * 2,
      recordBytesExchanged: 0,
    };

    // Phase 1: 4-byte digest comparison.
    if (this.table.digest === peer.table.digest) {
      return outcome;
    }
    outcome.inSync = false;

    // Phase 2: interval (bucket) digest map exchange.
    const mine = this.table.bucketDigests();
    const theirs = peer.table.bucketDigests();
    outcome.digestBytesExchanged += mine.length * DIGEST_BYTES * 2;
    const differing = new Set<number>();
    for (let bucket = 0; bucket < mine.length; bucket += 1) {
      if (mine[bucket] !== theirs[bucket]) {
        differing.add(bucket);
      }
    }
    outcome.differingBuckets = [...differing].sort((a, b) => a - b);

    // Phase 3: bidirectional pull of records in the differing intervals.
    const pushToPeer = this.table.recordsInBuckets(differing);
    const pullFromPeer = peer.table.recordsInBuckets(differing);
    outcome.recordBytesExchanged +=
      wireSize(pushToPeer) + wireSize(pullFromPeer);
    outcome.recordsPushed = peer.table.applyAll(pushToPeer).length;
    outcome.recordsPulled = this.table.applyAll(pullFromPeer).length;
    return outcome;
  }

  /** Probe every connected peer once; returns the per-peer outcomes. */
  probeCycle(): ProbeOutcome[] {
    const outcomes: ProbeOutcome[] = [];
    for (const peer of this.peers.values()) {
      outcomes.push(this.probe(peer));
    }
    return outcomes;
  }
}

function wireSize(records: MemberRecord[]): number {
  // id + status + 4-byte incarnation per record, roughly the gossip wire format.
  return records.reduce(
    (total, record) => total + record.id.length + record.status.length + 4,
    0,
  );
}
