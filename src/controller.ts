import type { Direction } from './geometry.ts';
import { FocusAnnouncer } from './announcer.ts';
import { logicalFallback, planNext } from './navigator.ts';
import type { LayoutTree, NodeId } from './tree.ts';

export type KeyResultStatus = 'focused' | 'noop' | 'stale' | 'giveup';

export interface KeyResult {
  status: KeyResultStatus;
  target: NodeId | null;
  reason?: string;
}

export const KEY_TO_DIRECTION: Record<string, Direction> = {
  ArrowUp: 'up',
  ArrowDown: 'down',
  ArrowLeft: 'left',
  ArrowRight: 'right',
};

/**
 * Orchestrates directional focus moves:
 *   1. plan a candidate from the current layout;
 *   2. request mount and/or scroll for the candidate;
 *   3. when async work completes, validate the layout version and
 *      re-check the candidate against the current layout;
 *   4. if the candidate was deleted or became invisible, re-plan with the
 *      failed candidate excluded (bounded, so it always terminates);
 *   5. a newer key press supersedes an older in-flight one (stale result
 *      is discarded, never committed).
 */
export class FocusController {
  focusId: NodeId | null = null;
  readonly announcer: FocusAnnouncer;
  readonly tree: LayoutTree;

  private readonly wrap: boolean;
  private seq = 0;
  private readonly inFlight = new Set<Promise<unknown>>();

  constructor(tree: LayoutTree, opts: { wrap?: boolean } = {}) {
    this.tree = tree;
    this.wrap = opts.wrap ?? true;
    this.announcer = new FocusAnnouncer(tree);
  }

  busy(): boolean {
    return this.inFlight.size > 0;
  }

  async awaitIdle(): Promise<void> {
    while (this.inFlight.size > 0) {
      await Promise.all([...this.inFlight]);
    }
  }

  handleKeyEvent(event: { key: string }): Promise<KeyResult> {
    const dir = KEY_TO_DIRECTION[event.key];
    if (!dir) return Promise.resolve({ status: 'noop', target: this.focusId });
    return this.handleKey(dir);
  }

  handleKey(dir: Direction): Promise<KeyResult> {
    const pending = this.handleKeyInner(dir);
    this.inFlight.add(pending);
    void pending.finally(() => this.inFlight.delete(pending));
    return pending;
  }

  /** Test/setup helper: place focus directly on a currently valid node. */
  setFocus(id: NodeId): void {
    if (!this.tree.isFocusableNow(id)) {
      throw new Error(`cannot set focus on invalid node "${id}"`);
    }
    this.focusId = id;
    this.announcer.announce(id, 'set');
  }

  /**
   * Deterministic recovery: if the focused node was deleted, collapsed or
   * otherwise became unfocusable, fall back to the first focusable-visible
   * node in logical order.
   */
  ensureFocus(): NodeId | null {
    if (this.focusId && this.tree.isFocusableNow(this.focusId)) return this.focusId;
    const next = logicalFallback(this.tree);
    this.focusId = next;
    if (next) this.announcer.announce(next, 'recovery');
    return next;
  }

  private async handleKeyInner(dir: Direction): Promise<KeyResult> {
    const token = ++this.seq;
    const failed = new Set<NodeId>();
    const maxAttempts = this.tree.size + 1;

    for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
      this.ensureFocus();
      if (!this.focusId) return { status: 'noop', target: null };

      const plan = planNext(this.tree, this.focusId, dir, { wrap: this.wrap, exclude: failed });
      if (!plan) return { status: 'noop', target: this.focusId };

      const versionAtPlan = this.tree.layoutVersion;
      const waits: Promise<unknown>[] = [];
      for (const mountId of plan.mountTargets) waits.push(this.tree.requestMount(mountId));
      if (plan.needsScroll) waits.push(this.tree.requestScrollTo(plan.target));
      if (waits.length > 0) await Promise.all(waits);

      if (token !== this.seq) {
        // A newer key press superseded this one; the slow result is expired.
        return { status: 'stale', target: this.focusId };
      }

      if (this.tree.layoutVersion === versionAtPlan) {
        // Layout untouched since planning: the plan is still authoritative.
        return this.commit(plan.target, plan.reason);
      }

      // Layout version changed while async work completed (scroll applied,
      // nodes mounted/deleted/moved): re-validate against the current layout.
      if (this.tree.isFocusableNow(plan.target)) {
        return this.commit(plan.target, plan.reason);
      }
      // Candidate deleted or no longer visible: exclude it and re-plan.
      failed.add(plan.target);
    }
    return { status: 'giveup', target: this.focusId };
  }

  private commit(id: NodeId, reason: string): KeyResult {
    this.focusId = id;
    this.announcer.announce(id, reason);
    return { status: 'focused', target: id, reason };
  }
}
