import assert from "node:assert/strict";
import { test } from "./harness.ts";

import {
  AccountAuthService,
  BfcacheHost,
  DeterministicEventLoop,
  MemoryOnlyDomSnapshot,
  createBfcacheHost,
  findShortestViolationTrace,
  findShortestBfcacheViolationTrace,
  createReviewHarness,
} from "../index.ts";
import type { AuthEpochEvent, PolicySnapshot, Partition } from "../index.ts";

const PARTITION: Partition = {
  topLevelSite: "https://app.example",
  origin: "https://app.example",
  browserSessionID: "browser-session-1",
};

class MutablePolicyAuthority {
  current: PolicySnapshot;
  reads = 0;

  constructor(generation = 1) {
    this.current = {
      generation,
      allowSensitiveDom: true,
      allowNetwork: true,
      allowFormSubmission: true,
      allowMessagePort: true,
      label: `policy-${generation}`,
    };
  }

  read(): PolicySnapshot {
    this.reads += 1;
    return this.current;
  }

  tighten(
    overrides: Partial<
      Pick<
        PolicySnapshot,
        | "allowSensitiveDom"
        | "allowNetwork"
        | "allowFormSubmission"
        | "allowMessagePort"
      >
    > = {},
  ): PolicySnapshot {
    this.current = {
      ...this.current,
      ...overrides,
      generation: this.current.generation + 1,
      label: `policy-${this.current.generation + 1}`,
    };
    return this.current;
  }
}

async function makeHost(options: {
  historyEntryID?: string;
  authSessionID?: string;
  accountID?: string;
  policy?: MutablePolicyAuthority;
  browserSessionID?: string;
  dom?: Record<string, string>;
} = {}): Promise<{
  auth: AccountAuthService;
  host: BfcacheHost;
  policy: MutablePolicyAuthority;
  authSessionID: string;
  activeEpoch: number;
  partition: Partition;
}> {
  const auth = await AccountAuthService.create();
  const authSessionID = options.authSessionID ?? "auth-session-1";
  const active = await auth.issueActive({
    accountID: options.accountID ?? "account-1",
    authSessionID,
  });
  const policy = options.policy ?? new MutablePolicyAuthority();
  const partition: Partition = {
    ...PARTITION,
    ...(options.browserSessionID
      ? { browserSessionID: options.browserSessionID }
      : {}),
  };
  const host = createBfcacheHost({
    browserSessionID: partition.browserSessionID,
    authAuthority: auth,
    policyAuthority: policy,
  });
  await host.navigate({
    historyEntryID: options.historyEntryID ?? "A",
    topLevelSite: partition.topLevelSite,
    origin: partition.origin,
    authSessionID,
    sensitiveDom: options.dom ?? { secret: "SENSITIVE-DOM-VALUE" },
  });
  return {
    auth,
    host,
    policy,
    authSessionID,
    activeEpoch: active.epoch,
    partition,
  };
}

test("Ed25519 auth epochs are signed, tamper-evident and revoke monotonically", async () => {
  const auth = await AccountAuthService.create();
  const active = await auth.issueActive({
    accountID: "account-1",
    authSessionID: "auth-session-1",
  });
  assert.equal(await auth.verify(active), true);

  const tamperedState = { ...active, state: "Active" as const, epoch: 2 };
  assert.equal(await auth.verify(tamperedState), false);
  const tamperedSignature = { ...active, signature: "AAAA" };
  assert.equal(await auth.verify(tamperedSignature), false);

  const otherService = await AccountAuthService.create();
  assert.equal(await otherService.verify(active), false);

  const revoked = await auth.revoke({
    authSessionID: "auth-session-1",
    reason: "logout",
  });
  assert.equal(revoked.epoch, active.epoch + 1);
  assert.equal(revoked.state, "Revoked");
  assert.equal(await auth.verify(revoked), true);
  await assert.rejects(
    auth.issueActive({
      accountID: "account-1",
      authSessionID: "auth-session-1",
    }),
    /revoked/,
  );
});

test("concurrent control drivers cannot interleave control tasks", async () => {
  const loop = new DeterministicEventLoop();
  const order: string[] = [];
  let release: () => void = () => {};
  loop.enqueue("control", "auth-a", async () => {
    order.push("auth-a-start");
    await new Promise<void>((resolve) => {
      release = resolve;
    });
    order.push("auth-a-end");
  });
  loop.enqueue("control", "pageshow-b", () => {
    order.push("pageshow-b");
  });
  loop.enqueue("document", "response", () => {
    order.push("response");
  });

  const first = loop.runControlUntilIdle();
  const second = loop.runControlUntilIdle();
  await Promise.resolve();
  assert.deepEqual(order, ["auth-a-start"]);
  release();
  await Promise.all([first, second]);
  assert.deepEqual(order, ["auth-a-start", "auth-a-end", "pageshow-b"]);
  assert.equal(loop.documentSize, 1);
  await loop.runUntilIdle();
  assert.deepEqual(order, [
    "auth-a-start",
    "auth-a-end",
    "pageshow-b",
    "response",
  ]);
});

test("concurrent push revocation and pageshow serialize on the control lane", async () => {
  const { auth, host, authSessionID, partition } = await makeHost();
  host.pagehide({ historyEntryID: "A", persisted: true });
  const revoked = await auth.revoke({ authSessionID, reason: "logout" });
  const revokePromise = host.receiveAuthEpoch(revoked);
  const showPromise = host.pageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: partition,
  });
  const [revokeResult, showResult] = await Promise.all([
    revokePromise,
    showPromise,
  ]);
  assert.equal(revokeResult.accepted, true);
  assert.equal(showResult.outcome, "discarded");
  assert.equal(showResult.reason, "auth-session-revoked");
  const authIndex = host.audit.findIndex((event) => event.type === "auth-epoch");
  const restoreIndex = host.audit.findIndex(
    (event) => event.type === "bfcache-restore-start",
  );
  assert.equal(authIndex >= 0 && authIndex < restoreIndex, true);
});

test("freeze blocks DOM, network, forms, ports and timers", async () => {
  const { host, partition } = await makeHost();
  let timerRuns = 0;
  host.setTimeout(() => {
    timerRuns += 1;
  }, 25);

  const freeze = host.pagehide({ historyEntryID: "A", persisted: true });
  assert.equal(freeze.state, "frozen");
  assert.equal(freeze.snapshotBytes! > 0, true);
  assert.equal(freeze.documentGeneration, 1);

  assert.equal(host.tryReadSensitive("secret").ok, false);
  assert.equal(host.tryReadSensitive("secret").reason, "document-frozen");
  assert.equal(host.submitForm("/transfer").ok, false);
  assert.throws(
    () => host.startNetworkRequest("https://api.example/private"),
    /denied/,
  );
  assert.throws(() => host.createMessagePort(), /document-frozen|cannot create/);
  assert.throws(() => host.setTimeout(() => {}, 10), /denied/);

  assert.equal(host.advanceTime(10_000), 0);
  assert.equal(timerRuns, 0);

  const freezeAudit = host.audit.find((event) => event.type === "bfcache-freeze");
  assert.equal(freezeAudit?.detail?.snapshotStorage, "memory");

  const resume = await host.pageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: partition,
  });
  assert.equal(resume.outcome, "resumed");
  assert.equal(host.tryReadSensitive("secret").ok, true);
  assert.equal(host.submitForm("/transfer").ok, true);
  host.advanceTime(25);
  await host.runUntilIdle();
  assert.equal(timerRuns, 1);
});

test("DOM snapshots are memory-only and cannot be serialized", () => {
  const snapshot = MemoryOnlyDomSnapshot.from(
    new Map([["selector", "secret-value"]]),
  );
  assert.equal(snapshot.storage, "memory");
  assert.equal(snapshot.read("selector"), "secret-value");
  assert.throws(() => JSON.stringify(snapshot), /memory-only/);
  snapshot.wipe();
  assert.equal(snapshot.wiped, true);
  assert.equal(snapshot.read("selector"), undefined);
});

test("successful restore delivers frozen network, message and promise events in arrival order", async () => {
  const { host, partition } = await makeHost();
  const order: string[] = [];
  const request = host.startNetworkRequest("https://api.example/private", () => {
    order.push("response");
  });
  const port = host.createMessagePort();
  port.onmessage = () => {
    order.push("message");
  };
  const promise = host.createPromise<string>();
  promise.then(() => {
    order.push("promise");
  });

  host.pagehide({ historyEntryID: "A", persisted: true });
  assert.equal(host.deliverNetworkResponse(request, "body").deferred, true);
  assert.equal(host.deliverMessage(port.id, "payload").deferred, true);
  assert.equal(promise.resolve("value").deferred, true);
  assert.equal(host.getDocument("A")?.deferredEvents, 3);

  const result = await host.pageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: partition,
  });
  assert.equal(result.outcome, "resumed");
  assert.equal(host.getDocument("A")?.hasInMemorySnapshot, false);
  await host.runUntilIdle();
  assert.deepEqual(order, ["response", "message", "promise"]);
  assert.equal(host.violations.length, 0);
});

test("revocation discards the frozen document, drops deferred work and fresh-navigates", async () => {
  const { auth, host, authSessionID, activeEpoch } = await makeHost();
  const oldRequest = host.startNetworkRequest("https://api.example/private", () => {
    throw new Error("old response handler must never run");
  });
  const events: string[] = [];
  host.addPageListener("pagehide", () => {
    events.push("pagehide");
  });
  host.addPageListener("pageshow", () => {
    events.push("pageshow");
  });

  host.pagehide({ historyEntryID: "A", persisted: true });
  host.deliverNetworkResponse(oldRequest, "late-body");
  assert.equal(host.getDocument("A")?.deferredEvents, 1);

  const revoked = await auth.revoke({
    authSessionID,
    reason: "logout",
  });
  assert.equal(revoked.epoch, activeEpoch + 1);
  const applied = await host.receiveAuthEpoch(revoked);
  assert.equal(applied.accepted, true);
  assert.equal(applied.state, "Revoked");
  assert.equal(host.getDocument("A")?.state, "frozen");
  assert.equal(host.getDocument("A")?.hasInMemorySnapshot, false);

  const result = await host.pageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: PARTITION,
  });
  assert.equal(result.outcome, "discarded");
  assert.equal(result.reason, "auth-session-revoked");
  assert.equal(result.previousDocumentGeneration, 1);
  assert.equal(result.documentGeneration, 2);
  assert.equal(result.droppedDeferred! >= 1, true);

  const old = host.getDocument(1);
  const fresh = host.getDocument(2);
  assert.equal(old?.state, "discarded");
  assert.equal(old?.hasInMemorySnapshot, false);
  assert.equal(fresh?.state, "locked");
  assert.equal(fresh?.identity.documentGeneration, 2);
  assert.equal(fresh?.identity.topLevelSite, PARTITION.topLevelSite);
  assert.equal(host.tryReadSensitive("secret", 1).ok, false);
  assert.equal(host.tryReadSensitive("secret", 2).ok, false);
  await host.runUntilIdle();
  assert.deepEqual(events, ["pagehide"]);
  assert.equal(host.violations.length, 0);
});

test("failed restore drops every deferred response, message and promise callback", async () => {
  const { auth, host, authSessionID, partition } = await makeHost();
  const effects: string[] = [];
  const request = host.startNetworkRequest("https://api.example/private", () => {
    effects.push("response");
  });
  const port = host.createMessagePort();
  port.onmessage = () => {
    effects.push("message");
  };
  const promise = host.createPromise<string>();
  promise.then(() => {
    effects.push("promise");
  });

  host.pagehide({ historyEntryID: "A", persisted: true });
  host.deliverNetworkResponse(request, "body");
  host.deliverMessage(port.id, "payload");
  promise.resolve("value");
  assert.equal(host.getDocument("A")?.deferredEvents, 3);

  const revoked = await auth.revoke({ authSessionID, reason: "logout" });
  await host.receiveAuthEpoch(revoked);
  const result = await host.pageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: partition,
  });
  assert.equal(result.outcome, "discarded");
  assert.equal(result.reason, "auth-session-revoked");
  assert.equal(result.droppedDeferred, 3);
  await host.runUntilIdle();
  assert.deepEqual(effects, []);
  assert.equal(host.getDocument(1)?.state, "discarded");
  assert.equal(host.getDocument(1)?.deferredEvents, 0);
  assert.equal(host.violations.length, 0);
});

test("same-session policy tightening rejects restoration", async () => {
  const { host, policy, partition } = await makeHost();
  const request = host.startNetworkRequest("https://api.example/private", () => {
    throw new Error("deferred response handler must be dropped");
  });
  host.pagehide({ historyEntryID: "A", persisted: true });
  host.deliverNetworkResponse(request, "body");

  const tightened = policy.tighten({ allowMessagePort: false });
  const result = await host.pageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: partition,
  });
  assert.equal(result.outcome, "discarded");
  assert.equal(result.reason, "policy-generation-mismatch");
  assert.equal(result.documentGeneration, 2);
  assert.equal(host.getDocument(1)?.state, "discarded");
  assert.equal(host.getDocument(2)?.identity.policyGeneration, tightened.generation);
  assert.equal(host.getDocument(2)?.state, "live");
  assert.equal(result.droppedDeferred, 1);
  assert.equal(host.violations.length, 0);
});

test("a policy fingerprint change without a new generation also fails closed", async () => {
  const { host, policy, partition } = await makeHost();
  host.pagehide({ historyEntryID: "A", persisted: true });
  // A buggy authority mutates permissions without allocating a generation.
  policy.current = {
    ...policy.current,
    allowSensitiveDom: false,
    allowNetwork: false,
  };
  const result = await host.pageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: partition,
  });
  assert.equal(result.outcome, "discarded");
  assert.equal(result.reason, "policy-fingerprint-mismatch");
  assert.equal(host.getDocument("A")?.identity.documentGeneration, 2);
  assert.equal(host.violations.length, 0);
});

test("a pushed policy update locks a visible document immediately", async () => {
  const { host, policy } = await makeHost();
  const next = policy.tighten({ allowSensitiveDom: false });
  const applied = await host.receivePolicy(next);
  assert.equal(applied.accepted, true);
  assert.equal(host.getDocument("A")?.state, "locked");
  assert.equal(host.tryReadSensitive("secret").ok, false);
  assert.equal(host.submitForm("/transfer").ok, false);
});

test("cross-site back traversal discards and fresh-navigates the original site", async () => {
  const { host } = await makeHost();
  host.pagehide({ historyEntryID: "A", persisted: true });
  const result = await host.pageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: {
      topLevelSite: "https://other.example",
      origin: "https://other.example",
      browserSessionID: PARTITION.browserSessionID,
    },
  });
  assert.equal(result.outcome, "discarded");
  assert.equal(result.reason, "top-level-site-mismatch");
  const fresh = host.getDocument("A");
  assert.equal(fresh?.state, "live");
  assert.equal(fresh?.identity.topLevelSite, PARTITION.topLevelSite);
  assert.equal(fresh?.identity.origin, PARTITION.origin);
  assert.equal(fresh?.identity.documentGeneration, 2);
  assert.equal(host.violations.length, 0);
});

test("a changed browser session cannot restore an old partition document", async () => {
  const { host } = await makeHost();
  host.pagehide({ historyEntryID: "A", persisted: true });
  const result = await host.pageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: {
      topLevelSite: PARTITION.topLevelSite,
      origin: PARTITION.origin,
      browserSessionID: "different-browser-session",
    },
  });
  assert.equal(result.outcome, "discarded");
  assert.equal(result.reason, "browser-session-mismatch");
  assert.equal(
    host.getDocument("A")?.identity.browserSessionID,
    "different-browser-session",
  );
});

test("an authority read failure during pageshow is fail-closed", async () => {
  const auth = await AccountAuthService.create();
  const authSessionID = "auth-session-flaky";
  await auth.issueActive({ accountID: "account-flaky", authSessionID });
  let failReads = false;
  const authority = {
    verify: (event: AuthEpochEvent) => auth.verify(event),
    readCurrent: (sessionID: string) => {
      if (failReads) {
        return Promise.reject(new Error("authority offline"));
      }
      return auth.readCurrent(sessionID);
    },
  };
  const policy = new MutablePolicyAuthority();
  const host = createBfcacheHost({
    browserSessionID: PARTITION.browserSessionID,
    authAuthority: authority,
    policyAuthority: policy,
  });
  await host.navigate({
    historyEntryID: "A",
    topLevelSite: PARTITION.topLevelSite,
    origin: PARTITION.origin,
    authSessionID,
  });
  host.pagehide({ historyEntryID: "A", persisted: true });
  failReads = true;
  const result = await host.pageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: PARTITION,
  });
  assert.equal(result.outcome, "discarded");
  assert.equal(result.reason, "authority-read-failed");
  assert.equal(host.getDocument("A")?.state, "locked");
  assert.equal(host.violations.length, 0);
});

test("an Active epoch advance invalidates older documents even without Revoked", async () => {
  const { auth, host, authSessionID, activeEpoch, partition } = await makeHost();
  host.pagehide({ historyEntryID: "A", persisted: true });
  const bumped = await auth.issueActive({
    accountID: "account-1",
    authSessionID,
    epoch: activeEpoch + 1,
  });
  await host.receiveAuthEpoch(bumped);
  assert.equal(host.getDocument(1)?.authorizationLost, true);
  const result = await host.pageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: partition,
  });
  assert.equal(result.outcome, "discarded");
  assert.equal(result.reason, "auth-epoch-mismatch");
  assert.equal(host.getDocument("A")?.identity.authEpoch, activeEpoch + 1);
  assert.equal(host.getDocument("A")?.state, "live");
  assert.equal(host.violations.length, 0);
});

test("multiple history entries are independently fenced and discarded", async () => {
  const { auth, host, authSessionID, partition } = await makeHost();
  await host.navigate({
    historyEntryID: "B",
    topLevelSite: partition.topLevelSite,
    origin: partition.origin,
    authSessionID,
    sensitiveDom: { secret: "OTHER" },
  });
  host.pagehide({ historyEntryID: "B", persisted: true });
  assert.equal(host.getDocument("A")?.state, "frozen");
  assert.equal(host.getDocument("B")?.state, "frozen");

  const revoked = await auth.revoke({ authSessionID, reason: "logout" });
  await host.receiveAuthEpoch(revoked);

  const b = await host.pageshow({
    historyEntryID: "B",
    persisted: true,
    currentPartition: partition,
  });
  assert.equal(b.outcome, "discarded");
  assert.equal(b.reason, "auth-session-revoked");
  assert.equal(b.documentGeneration, 3);

  const a = await host.pageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: partition,
  });
  assert.equal(a.outcome, "discarded");
  assert.equal(a.reason, "auth-session-revoked");
  assert.equal(a.documentGeneration, 4);

  assert.equal(host.getDocument(1)?.state, "discarded");
  assert.equal(host.getDocument(2)?.state, "discarded");
  assert.notEqual(host.getDocument(3)?.state, "live");
  assert.equal(host.getDocument(4)?.state, "locked");
  assert.equal(host.violations.length, 0);
});

test("re-navigating the same history entry retires the previous generation", async () => {
  const { host, authSessionID, partition } = await makeHost();
  await host.navigate({
    historyEntryID: "A",
    topLevelSite: partition.topLevelSite,
    origin: partition.origin,
    authSessionID,
    sensitiveDom: { secret: "REPLACEMENT" },
  });
  assert.notEqual(host.getDocument(1)?.state, "live");
  assert.equal(host.getDocument(2)?.state, "live");
  assert.equal(host.getDocument("A")?.identity.documentGeneration, 2);
  assert.equal(host.tryReadSensitive("secret", 1).ok, false);
  assert.equal(host.violations.length, 0);
});

test("a response that arrived before logout is not dispatched after revocation", async () => {
  const { auth, host, authSessionID } = await makeHost();
  const effects: string[] = [];
  const request = host.startNetworkRequest("https://api.example/private", () => {
    effects.push("response-handler");
    host.tryReadSensitive("secret");
  });

  assert.equal(
    host.deliverNetworkResponse(request, "body").queued,
    true,
  );
  const revoked = await auth.revoke({ authSessionID, reason: "logout" });
  // Queue the revocation on the control lane without draining document work.
  void host.enqueueAuthEpoch(revoked);
  await host.runUntilIdle();

  assert.deepEqual(effects, []);
  assert.equal(host.getDocument("A")?.state, "locked");
  assert.equal(host.tryReadSensitive("secret").ok, false);
  assert.equal(host.submitForm("/transfer").ok, false);
  assert.equal(host.violations.length, 0);
  const allowedAfterRevoke = host.audit.filter(
    (event) =>
      event.type === "document-task" &&
      event.result === "allowed" &&
      event.afterRevocation,
  );
  assert.equal(allowedAfterRevoke.length, 0);
});

test("no old task or pageshow script runs before or after a failed restore", async () => {
  const { auth, host, authSessionID, partition } = await makeHost();
  const order: string[] = [];
  host.addPageListener("pagehide", () => {
    order.push("pagehide-listener");
  });
  host.addPageListener("pageshow", () => {
    order.push("pageshow-listener");
  });
  const request = host.startNetworkRequest("https://api.example/private", () => {
    order.push("response-handler");
  });

  // Arrive while live, but do not let the document lane run yet.
  host.deliverNetworkResponse(request, "body");
  host.pagehide({ historyEntryID: "A", persisted: true });
  const revoked = await auth.revoke({ authSessionID, reason: "password-reset" });
  void host.enqueueAuthEpoch(revoked);
  void host.enqueuePageshow({
    historyEntryID: "A",
    persisted: true,
    currentPartition: partition,
  });
  await host.runUntilIdle();

  assert.deepEqual(order, ["pagehide-listener"]);
  assert.equal(host.getDocument(1)?.state, "discarded");
  assert.equal(host.violations.length, 0);
});

test("tampered AuthEpoch events are rejected without changing the session", async () => {
  const { auth, host, authSessionID } = await makeHost();
  const revoked = await auth.revoke({ authSessionID, reason: "logout" });
  const tampered = { ...revoked, epoch: revoked.epoch + 1 };
  const result = await host.receiveAuthEpoch(tampered);
  assert.equal(result.accepted, false);
  assert.equal(result.reason, "signature-invalid");
  assert.equal(host.authSessionView(authSessionID)?.state, "Active");
  assert.equal(host.getDocument("A")?.state, "live");
});

test("replayed old Active epochs cannot resurrect a revoked session", async () => {
  const { auth, host, authSessionID, activeEpoch } = await makeHost();
  const active = auth.peek(authSessionID)!;
  const revoked = await auth.revoke({ authSessionID, reason: "logout" });
  await host.receiveAuthEpoch(revoked);
  const replay = await host.receiveAuthEpoch(active);
  assert.equal(replay.accepted, false);
  assert.equal(replay.reason, "stale-epoch");
  assert.equal(host.authSessionView(authSessionID)?.state, "Revoked");
  assert.equal(host.authSessionView(authSessionID)?.epoch, activeEpoch + 1);
});

test("trace search finds the shortest delivery-after-revocation bug", async () => {
  const trace = await findShortestViolationTrace(
    () =>
      createReviewHarness({
        reviewGuards: {
          quarantineOnRevokedEvent: false,
          checkDeliveryAgainstAuth: false,
        },
      }),
    { maxDepth: 4, maxStates: 20000 },
  );
  assert.ok(trace);
  assert.equal(trace.depth, 3);
  assert.deepEqual(trace.actions, ["revoke", "lateResponse", "drain"]);
  assert.equal(trace.violation.afterRevocation, true);
});

test("trace search finds the shortest restore-without-auth-revalidation bug", async () => {
  const trace = await findShortestViolationTrace(
    () =>
      createReviewHarness({
        reviewGuards: {
          quarantineOnRevokedEvent: true,
          purgeOnRevokedEvent: false,
          checkDeliveryAgainstAuth: true,
          authorizeSensitiveEffects: false,
          revalidateOnResume: false,
        },
      }),
    { maxDepth: 6, maxStates: 50000 },
  );
  assert.ok(trace);
  assert.equal(trace.depth, 5);
  assert.deepEqual(trace.actions, [
    "pagehide",
    "revoke",
    "pageshow",
    "drain",
    "domRead",
  ]);
  assert.equal(trace.violation.type, "sensitive-dom-read");
});

test("trace search also detects a network submission after revocation", async () => {
  const trace = await findShortestViolationTrace(
    () =>
      createReviewHarness({
        reviewGuards: {
          quarantineOnRevokedEvent: false,
          authorizeSensitiveEffects: false,
          checkDeliveryAgainstAuth: true,
          revalidateOnResume: true,
        },
      }),
    {
      actions: ["revoke", "drain", "networkSubmit"],
      maxDepth: 3,
      maxStates: 1000,
    },
  );
  assert.ok(trace);
  assert.deepEqual(trace.actions, ["revoke", "drain", "networkSubmit"]);
  assert.equal(trace.violation.type, "network-submit");
  assert.equal(trace.violation.afterRevocation, true);
});

test("BFCache-constrained trace search interleaves pagehide, revocation and pageshow", async () => {
  const trace = await findShortestBfcacheViolationTrace(
    () =>
      createReviewHarness({
        reviewGuards: {
          quarantineOnRevokedEvent: false,
          purgeOnRevokedEvent: false,
          checkDeliveryAgainstAuth: false,
          authorizeSensitiveEffects: false,
          revalidateOnResume: false,
        },
      }),
    { maxDepth: 6, maxStates: 50000 },
  );
  assert.ok(trace);
  assert.equal(trace.depth, 5);
  assert.equal(trace.actions.includes("pagehide"), true);
  assert.equal(trace.actions.includes("pageshow"), true);
  assert.equal(trace.actions.includes("revoke"), true);
  assert.equal(trace.actions.includes("lateResponse"), true);
  assert.equal(trace.violation.afterRevocation, true);
});

test("the production guard set has no violating trace within the search bound", async () => {
  const trace = await findShortestViolationTrace(
    () => createReviewHarness(),
    { maxDepth: 5, maxStates: 20000 },
  );
  assert.equal(trace, null);
});
