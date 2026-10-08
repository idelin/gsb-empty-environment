export interface Route {
  path: string;
}

/**
 * What a guard may return:
 *  - `undefined` / `true`      -> allow this target
 *  - `false`                   -> reject (cancel) the whole navigation
 *  - `"/path"` / `{ redirect }`-> restart the guard chain for a new target
 */
export type GuardReturn = void | boolean | string | { redirect: string };

export type Guard = (
  to: Route,
  from: Route,
) => GuardReturn | Promise<GuardReturn>;

export type CancelReason =
  | "rejected" // a guard returned false
  | "superseded" // a newer navigation invalidated this one
  | "redirect-loop" // a target was visited twice in the same round
  | "out-of-range"; // back/forward/go past the history bounds

export type NavigationResult =
  | { status: "committed"; route: Route }
  | { status: "cancelled"; reason: CancelReason };

type Decision =
  | { type: "allow" }
  | { type: "reject" }
  | { type: "redirect"; to: string };

function normalize(raw: GuardReturn): Decision {
  if (raw === false) return { type: "reject" };
  if (typeof raw === "string") return { type: "redirect", to: raw };
  if (raw !== undefined && raw !== true) return { type: "redirect", to: raw.redirect };
  return { type: "allow" };
}

const ALLOW: Decision = { type: "allow" };
const REJECT: Decision = { type: "reject" };

type Intent =
  | { mode: "push" | "replace"; target: string }
  | { mode: "traverse"; index: number };

export class Router {
  private entries: Route[];
  private cursor = 0;
  private guards: Guard[] = [];
  /** Monotonic token; the latest navigation round owns the only valid one. */
  private navToken = 0;

  constructor(initial: string = "/") {
    this.entries = [{ path: initial }];
  }

  get current(): Route {
    return this.entries[this.cursor];
  }

  get index(): number {
    return this.cursor;
  }

  get canGoBack(): boolean {
    return this.cursor > 0;
  }

  get canGoForward(): boolean {
    return this.cursor < this.entries.length - 1;
  }

  /** Snapshot of the history stack. */
  history(): Route[] {
    return this.entries.slice();
  }

  /** Register a guard; runs in registration order. Returns an unregister fn. */
  beforeEach(guard: Guard): () => void {
    this.guards.push(guard);
    return () => {
      const at = this.guards.indexOf(guard);
      if (at >= 0) this.guards.splice(at, 1);
    };
  }

  navigate(path: string, options?: { replace?: boolean }): Promise<NavigationResult> {
    return this.run({ mode: options?.replace ? "replace" : "push", target: path });
  }

  back(): Promise<NavigationResult> {
    return this.go(-1);
  }

  forward(): Promise<NavigationResult> {
    return this.go(1);
  }

  go(delta: number): Promise<NavigationResult> {
    const index = this.cursor + delta;
    if (index < 0 || index >= this.entries.length) {
      return Promise.resolve({ status: "cancelled", reason: "out-of-range" });
    }
    return this.run({ mode: "traverse", index });
  }

  private async run(intent: Intent): Promise<NavigationResult> {
    const token = ++this.navToken;
    const from = this.current;

    let mode: "push" | "replace" | "traverse" = intent.mode;
    let traverseIndex = intent.mode === "traverse" ? intent.index : -1;
    let target =
      intent.mode === "traverse" ? this.entries[intent.index].path : intent.target;

    // Targets already visited during THIS round (redirects included).
    const visited = new Set<string>();

    for (;;) {
      if (visited.has(target)) {
        return { status: "cancelled", reason: "redirect-loop" };
      }
      visited.add(target);

      const decision = await this.runGuards(target, from, token);
      if (token !== this.navToken) {
        // A newer navigation started while our guards were in flight;
        // whatever the late promise resolved to must not move the address.
        return { status: "cancelled", reason: "superseded" };
      }
      if (decision.type === "reject") {
        return { status: "cancelled", reason: "rejected" };
      }
      if (decision.type === "redirect") {
        target = decision.to;
        if (mode === "traverse") {
          // Redirecting a back/forward traversal turns it into a fresh push.
          mode = "push";
          traverseIndex = -1;
        }
        continue;
      }

      // All guards allowed: the single commit point for the whole round.
      this.commit(mode, target, traverseIndex);
      return { status: "committed", route: { path: target } };
    }
  }

  /** Guards run strictly one after another; short-circuits on reject/redirect. */
  private async runGuards(target: string, from: Route, token: number): Promise<Decision> {
    const to: Route = { path: target };
    for (const guard of this.guards.slice()) {
      const decision = normalize(await guard(to, from));
      if (token !== this.navToken) return ALLOW; // stale; caller re-checks token
      if (decision.type !== "allow") return decision;
    }
    return ALLOW;
  }

  private commit(mode: "push" | "replace" | "traverse", target: string, index: number): void {
    if (mode === "traverse") {
      this.cursor = index;
      return;
    }
    if (mode === "replace") {
      this.entries[this.cursor] = { path: target };
      return;
    }
    // push: drop any forward entries, then append exactly one record.
    this.entries.length = this.cursor + 1;
    this.entries.push({ path: target });
    this.cursor += 1;
  }
}
