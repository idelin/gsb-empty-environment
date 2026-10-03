import type { Cluster, ClusterSnapshot } from './cluster.ts';
import { inspect } from './inspector.ts';
import type { InvariantViolation } from './inspector.ts';
import type { ClusterEvent, NodeId } from './types.ts';

/**
 * Invariant assertion instrument. Attaches to a live cluster and
 * re-validates the safety invariants after every delivered message and
 * every cluster event, so violations are caught at the exact transition
 * that causes them rather than at the end of a run.
 */
export class InvariantMonitor {
  private violations: InvariantViolation[] = [];
  private leadersByTerm = new Map<number, NodeId>();
  private leadershipHistory: { term: number; nodeId: NodeId }[] = [];
  private committedValues = new Map<number, string>();
  private unsubscribe: () => void;
  private cluster: Cluster;
  private attached = true;
  private seen = new Set<string>();
  checksRun = 0;

  constructor(cluster: Cluster) {
    this.cluster = cluster;
    this.unsubscribe = cluster.onEvent((e) => this.onEvent(e));
    cluster.net.setAfterDeliver(() => this.checkAll());
  }

  private onEvent(e: ClusterEvent): void {
    if (!this.attached) return;
    if (e.type === 'leader') {
      const key = e.term * 4096 + e.nodeId.charCodeAt(0);
      const prev = this.leadersByTerm.get(key);
      if (prev !== undefined) {
        this.push({
          invariant: 'I1 election-safety',
          detail: `ballot (${e.term},${e.nodeId}) claimed leadership more than once`,
        });
      }
      this.leadersByTerm.set(key, e.nodeId);
      this.leadershipHistory.push({ term: e.term, nodeId: e.nodeId });
    } else if (e.type === 'commit') {
      const prev = this.committedValues.get(e.slot);
      if (prev !== undefined && prev !== e.value) {
        this.push({
          invariant: 'I3 state-machine-safety',
          detail: `slot ${e.slot} committed as '${prev}' and later as '${e.value}' (node ${e.nodeId})`,
        });
      }
      this.committedValues.set(e.slot, e.value);
    }
  }

  /** Re-run the full invariant suite over a fresh snapshot. */
  checkAll(): void {
    if (!this.attached) return;
    this.checksRun++;
    const snap: ClusterSnapshot = this.cluster.snapshot();
    const report = inspect(snap, {
      leadershipHistory: this.leadershipHistory,
      // Structural skew is expected mid-flight in a live system; the
      // monitor only enforces hard safety invariants online.
      maxSizeRatio: Infinity,
      maxAbsLag: Infinity,
      maxDirtyShare: 1,
    });
    for (const v of report.invariantViolations) this.push(v);
    for (const d of report.danglingRefs) {
      // Online, only hard structural faults of the committed prefix are
      // violations; sparse acceptor logs (holes below in-flight slots) are
      // a legitimate transient that the offline inspector audits instead.
      if (d.kind !== 'commit-pointer' && d.kind !== 'lease') continue;
      this.push({
        invariant: 'I6 no-dangling-references',
        detail: `${d.nodeId}: ${d.kind}: ${d.detail}`,
      });
    }
  }

  private push(v: InvariantViolation): void {
    const key = `${v.invariant}|${v.detail}`;
    if (this.seen.has(key)) return;
    this.seen.add(key);
    this.violations.push(v);
  }

  getViolations(): InvariantViolation[] {
    return [...this.violations];
  }

  /** Assert-style accessor: throws with full detail on any violation. */
  assertClean(): void {
    if (this.violations.length > 0) {
      const lines = this.violations
        .map((v) => `  [${v.invariant}] ${v.detail}`)
        .join('\n');
      throw new Error(
        `invariant monitor recorded ${this.violations.length} violation(s):\n${lines}`,
      );
    }
  }

  /** Detach hooks so no references leak after the run. */
  dispose(): void {
    this.attached = false;
    this.unsubscribe();
    this.cluster.net.setAfterDeliver(null);
    this.violations = [];
    this.seen.clear();
    this.leadersByTerm.clear();
    this.leadershipHistory = [];
    this.committedValues.clear();
  }
}
