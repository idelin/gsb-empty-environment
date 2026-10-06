/**
 * Guarded navigation state library.
 *
 * - Guards run sequentially; each may allow, deny, or redirect.
 * - Starting a new navigation invalidates the previous one: late-arriving
 *   guard promises from a stale round never mutate the address or history.
 * - Redirect loops are detected against the targets visited in the current
 *   navigation round only (never against global history).
 * - History is committed exactly once, only when the final target is
 *   confirmed by every guard.
 */

export type GuardDecision =
  | { type: "allow" }
  | { type: "deny"; reason?: string }
  | { type: "redirect"; to: string };

/** What a guard may return: void/true = allow, false = deny, string = redirect target. */
export type GuardResult = void | boolean | string | GuardDecision;

export type Guard = (
  to: string,
  from: string,
) => GuardResult | Promise<GuardResult>;

export type NavOutcome =
  | { status: "committed"; path: string }
  | { status: "cancelled"; target: string; reason?: string }
  | { status: "superseded" }
  | { status: "loop"; chain: string[] };

function normalize(result: GuardResult): GuardDecision {
  if (result === undefined || result === true) return { type: "allow" };
  if (result === false) return { type: "deny" };
  if (typeof result === "string") return { type: "redirect", to: result };
  return result;
}

type CommitMode = "push" | "back";

export class Navigator {
  private guards: Guard[] = [];
  private entries: string[];
  private index: number;
  /** Monotonic id of the latest navigation round; older rounds are stale. */
  private navSeq = 0;

  constructor(initial: string) {
    this.entries = [initial];
    this.index = 0;
  }

  /** The committed address. Only changes on a successful commit. */
  get current(): string {
    return this.entries[this.index];
  }

  /** Read-only view of the committed history entries. */
  get history(): readonly string[] {
    return this.entries;
  }

  get canGoBack(): boolean {
    return this.index > 0;
  }

  /** Register a guard. Returns an unregister function. */
  addGuard(guard: Guard): () => void {
    this.guards.push(guard);
    return () => {
      const at = this.guards.indexOf(guard);
      if (at >= 0) this.guards.splice(at, 1);
    };
  }

  navigate(to: string): Promise<NavOutcome> {
    return this.startNavigation(to, "push");
  }

  back(): Promise<NavOutcome> {
    if (!this.canGoBack) {
      return Promise.resolve({
        status: "cancelled",
        target: this.current,
        reason: "no-history",
      });
    }
    return this.startNavigation(this.entries[this.index - 1], "back");
  }

  private async startNavigation(
    initialTarget: string,
    mode: CommitMode,
  ): Promise<NavOutcome> {
    const navId = ++this.navSeq;
    const isStale = () => navId !== this.navSeq;
    // Targets visited in *this* round; used for redirect-loop detection.
    const visited = new Set<string>();
    let target = initialTarget;

    while (true) {
      if (visited.has(target)) {
        return { status: "loop", chain: [...visited, target] };
      }
      visited.add(target);

      const decision = await this.runGuards(target, isStale);
      if (isStale()) return { status: "superseded" };

      if (decision.type === "deny") {
        return { status: "cancelled", target, reason: decision.reason };
      }
      if (decision.type === "redirect") {
        target = decision.to;
        continue;
      }
      // All guards allowed the final target: commit exactly once.
      this.commit(target, mode);
      return { status: "committed", path: target };
    }
  }

  private async runGuards(
    to: string,
    isStale: () => boolean,
  ): Promise<GuardDecision> {
    const from = this.current;
    for (const guard of this.guards) {
      const decision = normalize(await guard(to, from));
      // A newer navigation may have started while this guard was pending;
      // bail out before consulting any further guards.
      if (isStale()) return { type: "deny", reason: "superseded" };
      if (decision.type !== "allow") return decision;
    }
    return { type: "allow" };
  }

  private commit(target: string, mode: CommitMode): void {
    if (mode === "back" && this.entries[this.index - 1] === target) {
      this.index -= 1;
      return;
    }
    // Push mode, or a back-navigation whose guards redirected away from the
    // previous entry: truncate forward entries, then push the address once.
    this.entries = this.entries.slice(0, this.index + 1);
    this.entries.push(target);
    this.index += 1;
  }
}
