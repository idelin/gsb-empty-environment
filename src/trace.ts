import { UnsafeUnverifiedAuthAuthority } from "./auth.ts";
import { BfcacheHost, createBfcacheHost } from "./host.ts";
import type { ReviewGuards } from "./host.ts";
import type {
  AuditEvent,
  AuthEpochEvent,
  Partition,
  PolicySnapshot,
} from "./types.ts";

export type TraceAction =
  | "pagehide"
  | "revoke"
  | "pageshow"
  | "lateResponse"
  | "message"
  | "promise"
  | "domRead"
  | "formSubmit"
  | "networkSubmit"
  | "drain";

export const DEFAULT_TRACE_ACTIONS: readonly TraceAction[] = Object.freeze([
  "pagehide",
  "revoke",
  "pageshow",
  "lateResponse",
  "message",
  "promise",
  "domRead",
  "formSubmit",
  "networkSubmit",
  "drain",
]);

export interface TraceHarness {
  apply(action: TraceAction): void | Promise<void>;
  violation(): AuditEvent | undefined;
  stateKey(): string;
}

export type TraceFactory =
  | (() => TraceHarness | Promise<TraceHarness>);

export interface TraceSearchOptions {
  readonly maxDepth?: number;
  readonly maxStates?: number;
  readonly actions?: readonly TraceAction[];
  /**
   * Optional action set which a violating trace must contain.  Useful when
   * reviewing the BFCache path specifically instead of any early-delivery bug.
   */
  readonly requiredActions?: readonly TraceAction[];
}

export interface ViolationTrace {
  readonly depth: number;
  readonly actions: readonly TraceAction[];
  readonly violation: AuditEvent;
}

/**
 * Breadth-first search for the shortest action sequence which makes a
 * candidate host perform a sensitive effect after its session was revoked.
 *
 * Each prefix is replayed in a fresh host, so the search observes exactly the
 * same deterministic state machine as production.  State deduplication keeps
 * the branching factor bounded at the depths used by the regression tests.
 */
export async function findShortestViolationTrace(
  factory: TraceFactory,
  options: TraceSearchOptions = {},
): Promise<ViolationTrace | null> {
  const maxDepth = options.maxDepth ?? 7;
  const maxStates = options.maxStates ?? 50000;
  const actions = options.actions ?? DEFAULT_TRACE_ACTIONS;
  const requiredActions = options.requiredActions ?? [];

  let frontier: TraceAction[][] = [[]];
  const visited = new Set<string>();
  let replayed = 0;

  for (let depth = 0; depth <= maxDepth; depth += 1) {
    const next: TraceAction[][] = [];
    for (const trace of frontier) {
      if (replayed >= maxStates) {
        return null;
      }
      const harness = await factory();
      for (const action of trace) {
        await harness.apply(action);
      }
      replayed += 1;

      const violation = harness.violation();
      const satisfiesRequired = requiredActions.every((action) =>
        trace.includes(action),
      );
      if (violation && satisfiesRequired) {
        return { depth: trace.length, actions: trace, violation };
      }

      const key = harness.stateKey();
      if (!violation) {
        if (visited.has(key)) {
          continue;
        }
        visited.add(key);
      }

      if (depth === maxDepth) {
        continue;
      }
      for (const action of actions) {
        next.push([...trace, action]);
      }
    }
    frontier = next;
  }
  return null;
}

/**
 * Restrict the search to traces which actually exercise both BFCache
 * `pagehide` and `pageshow`.  This distinguishes a restore-validation flaw
 * from an unrelated early-delivery flaw in the same candidate.
 */
export function findShortestBfcacheViolationTrace(
  factory: TraceFactory,
  options: TraceSearchOptions = {},
): Promise<ViolationTrace | null> {
  return findShortestViolationTrace(factory, {
    ...options,
    requiredActions: ["pagehide", "pageshow"],
  });
}

export interface ReviewHarnessOptions {
  readonly reviewGuards?: Partial<ReviewGuards>;
  readonly authSessionID?: string;
  readonly accountID?: string;
  readonly historyEntryID?: string;
}

const REVIEW_PARTITION: Partition = Object.freeze({
  topLevelSite: "https://app.example",
  origin: "https://app.example",
  browserSessionID: "browser-session-1",
});

const REVIEW_POLICY: PolicySnapshot = Object.freeze({
  generation: 1,
  allowSensitiveDom: true,
  allowNetwork: true,
  allowFormSubmission: true,
  allowMessagePort: true,
  label: "baseline",
});

/**
 * A deterministic scenario used by the differential tests.
 *
 * The page holds a sensitive DOM value, an in-flight request whose handler
 * reads it, a MessagePort handler, and a guarded promise callback.  Those are
 * exactly the old-document effects that must never run after revocation.
 */
export interface ReviewHarness extends TraceHarness {
  readonly host: BfcacheHost;
  readonly auth: UnsafeUnverifiedAuthAuthority;
  readonly historyEntryID: string;
  readonly authSessionID: string;
  readonly requestID: string;
  readonly portID: string;
  readonly promiseID: string;
}

export async function createReviewHarness(
  options: ReviewHarnessOptions = {},
): Promise<ReviewHarness> {
  const authSessionID = options.authSessionID ?? "auth-session-1";
  const accountID = options.accountID ?? "account-1";
  const historyEntryID = options.historyEntryID ?? "A";

  const auth = new UnsafeUnverifiedAuthAuthority();
  auth.issue(accountID, authSessionID, 1, "Active");
  const policyAuthority = {
    read(): PolicySnapshot {
      return REVIEW_POLICY;
    },
  };
  const host = createBfcacheHost({
    browserSessionID: REVIEW_PARTITION.browserSessionID,
    authAuthority: auth,
    policyAuthority,
    ...(options.reviewGuards ? { reviewGuards: options.reviewGuards } : {}),
  });
  await host.navigate({
    historyEntryID,
    topLevelSite: REVIEW_PARTITION.topLevelSite,
    origin: REVIEW_PARTITION.origin,
    authSessionID,
    sensitiveDom: { secret: "sensitive-value" },
  });
  host.setSensitiveToken("memory-token", "token-value");

  let requestID = "";
  requestID = host.startNetworkRequest(
    "https://api.example/private",
    () => {
      host.tryReadSensitive("secret");
    },
  );

  const port = host.createMessagePort();
  const portID = port.id;
  port.onmessage = () => {
    host.tryReadSensitive("secret");
  };

  const promise = host.createPromise<string>();
  const promiseID = promise.id;
  promise.then(() => {
    host.tryReadSensitive("secret");
  });

  const revokeEvent: { current?: AuthEpochEvent } = {};

  const harness: ReviewHarness = {
    host,
    auth,
    historyEntryID,
    authSessionID,
    requestID,
    portID,
    promiseID,
    apply(action: TraceAction): void | Promise<void> {
      switch (action) {
        case "pagehide":
          try {
            host.pagehide({ historyEntryID, persisted: true });
          } catch {
            // No live/frozen document remains in this branch.
          }
          return;
        case "revoke":
          revokeEvent.current ??= auth.issue(
            accountID,
            authSessionID,
            2,
            "Revoked",
          );
          void host.enqueueAuthEpoch(revokeEvent.current);
          return;
        case "pageshow":
          void host.enqueuePageshow({
            historyEntryID,
            persisted: true,
            currentPartition: REVIEW_PARTITION,
          });
          return;
        case "lateResponse":
          host.deliverNetworkResponse(requestID, "private-response");
          return;
        case "message":
          host.deliverMessage(portID, "private-message");
          return;
        case "promise":
          promise.resolve("private-value");
          return;
        case "domRead":
          host.tryReadSensitive("secret");
          return;
        case "formSubmit":
          try {
            host.submitForm("/transfer");
          } catch {
            // Denial is the expected hardened outcome.
          }
          return;
        case "networkSubmit":
          try {
            host.startNetworkRequest("https://api.example/private");
          } catch {
            // Denial is the expected hardened outcome.
          }
          return;
        case "drain":
          return host.runUntilIdle();
        default:
          throw new Error(`unknown trace action ${String(action)}`);
      }
    },
    violation(): AuditEvent | undefined {
      return host.violations[0];
    },
    stateKey(): string {
      const document = host.getDocument(historyEntryID);
      const loop = host.eventLoop;
      return JSON.stringify([
        document?.identity.documentGeneration,
        document?.state,
        document?.authorized,
        document?.authorizationLost,
        document?.policyStale,
        document?.hasInMemorySnapshot,
        document?.deferredEvents,
        document?.deferredKinds,
        document?.pendingRequests,
        document?.openPorts,
        document?.activeTimers,
        host.domFingerprint("secret", historyEntryID),
        host.requestView(requestID)?.state,
        host.promiseState(promiseID),
        host.portState(portID),
        host.authSessionView(authSessionID),
        loop.controlSize,
        loop.documentSize,
        loop.pending().map((task) => task.label),
      ]);
    },
  };
  return harness;
}
