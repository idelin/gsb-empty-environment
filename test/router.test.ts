import test from "node:test";
import assert from "node:assert/strict";

import {
  BroadcastRouter,
  makePartition,
} from "../src/index.ts";
import type { PartitionIdentity, PartitionInput } from "../src/index.ts";

function partition(overrides: PartitionInput = {}): PartitionIdentity {
  return makePartition({
    topLevelRegistrableDomain: "example.com",
    initiatorOrigin: "https://app.example.com",
    crossSiteAncestor: false,
    configVersion: "2026-11-09",
    ...overrides,
  });
}

function activeDocument(
  router: BroadcastRouter,
  instanceId: string,
  displayUrl: string,
  identity: PartitionIdentity,
  ancestorChain: readonly string[] = [],
): void {
  router.createDocument({
    instanceId,
    displayUrl,
    partition: identity,
    ancestorChain,
    state: "active",
  });
}

test("same URL pages with different ancestor partitions only receive compatible broadcasts", () => {
  const router = new BroadcastRouter();
  const local = partition();
  const crossSite = partition({ crossSiteAncestor: true });
  const url = "https://app.example.com/shared/page";

  activeDocument(router, "sender", url, local, [
    "https://app.example.com",
    "https://app.example.com",
  ]);
  activeDocument(router, "same-site-frame", url, local, [
    "https://app.example.com",
    "https://app.example.com",
  ]);
  activeDocument(router, "cross-site-frame", url, crossSite, [
    "https://app.example.com",
    "https://identity.example.net",
  ]);

  const delivered: Array<[string, unknown]> = [];
  router.subscribe("same-site-frame", "room", {
    id: "sub-local",
    onMessage: (delivery) => delivered.push(["local", delivery.payload]),
  });
  router.subscribe("cross-site-frame", "room", {
    id: "sub-cross",
    onMessage: (delivery) => delivered.push(["cross", delivery.payload]),
  });

  const first = router.send("sender", "room", "visible");
  assert.equal(first.ingest?.status, "accepted");
  assert.deepEqual(delivered, [["local", "visible"]]);
  assert.ok(
    first.ingest?.skipped.some(
      (skip) =>
        skip.instanceId === "cross-site-frame" &&
        skip.reason === "send-partition-mismatch",
    ),
  );

  const second = router.send("sender", "room", "wrong-delivery-snapshot", {
    deliver: false,
  });
  const mismatch = router.transmit(second.envelope, {
    deliverySnapshot: crossSite,
  });
  assert.equal(mismatch.deliveries.length, 0);
  assert.ok(
    mismatch.skipped.some((skip) => skip.reason === "delivery-partition-mismatch"),
  );
  assert.deepEqual(delivered, [["local", "visible"]]);
});

test("configuration version and subscription visibility snapshots are enforced", () => {
  const router = new BroadcastRouter();
  const v1 = partition();
  const v2 = partition({ configVersion: "v2" });

  activeDocument(router, "sender", "https://app.example.com/", v1);
  activeDocument(router, "v1-page", "https://app.example.com/shared", v1);
  activeDocument(router, "v2-page", "https://app.example.com/shared", v2);

  const delivered: unknown[] = [];
  router.subscribe("v1-page", "room", {
    id: "hidden-scope",
    visibleOrigins: ["https://other.example.com"],
    onMessage: (delivery) => delivered.push(delivery.payload),
  });
  router.subscribe("v2-page", "room", {
    id: "version-scope",
    onMessage: (delivery) => delivered.push(delivery.payload),
  });

  const hidden = router.send("sender", "room", "hidden");
  assert.equal(hidden.ingest?.deliveries.length, 0);
  assert.ok(
    hidden.ingest?.skipped.some(
      (skip) => skip.subscriptionId === "hidden-scope" &&
        skip.reason === "outside-visible-scope",
    ),
  );
  assert.ok(
    hidden.ingest?.skipped.some(
      (skip) => skip.subscriptionId === "version-scope" &&
        skip.reason === "send-partition-mismatch",
    ),
  );

  router.unsubscribe("hidden-scope");
  router.subscribe("v1-page", "room", {
    id: "visible-scope",
    visibleOrigins: ["https://app.example.com"],
    onMessage: (delivery) => delivered.push(delivery.payload),
  });
  router.send("sender", "room", "visible");
  assert.deepEqual(delivered, ["visible"]);
});

test("prerender cannot receive messages sent before activation", () => {
  const router = new BroadcastRouter();
  const identity = partition();
  activeDocument(router, "sender", "https://app.example.com/", identity);
  router.createDocument({
    instanceId: "prerendered",
    displayUrl: "https://app.example.com/target",
    partition: identity,
    state: "prerender",
  });

  const received: unknown[] = [];
  router.subscribe("prerendered", "room", {
    id: "prerender-sub",
    onMessage: (delivery) => received.push(delivery.payload),
  });

  router.send("sender", "room", "before-activation");
  assert.deepEqual(received, []);

  const activated = router.activateDocument("prerendered");
  assert.equal(activated.state, "active");
  router.send("sender", "room", "after-activation");
  assert.deepEqual(received, ["after-activation"]);
});

test("navigation can keep URL and channel labels but never subscriptions", () => {
  const router = new BroadcastRouter();
  const identity = partition();
  activeDocument(router, "sender", "https://app.example.com/", identity);
  activeDocument(router, "page", "https://app.example.com/room", identity);

  const received: unknown[] = [];
  router.subscribe("page", "room", {
    id: "old-sub",
    onMessage: (delivery) => received.push(delivery.payload),
  });
  const before = router.getDocument("page");

  const after = router.navigateDocument("page", {
    instanceId: "page-next",
    keepDisplayUrl: true,
    keepChannelNames: true,
  });

  assert.equal(after.displayUrl, before.displayUrl);
  assert.ok(after.declaredChannelNames.includes("room"));
  assert.deepEqual(after.subscriptionIds, []);
  assert.notEqual(after.lifecycleId, before.lifecycleId);
  assert.equal(router.getDocument("page").state, "closed");

  router.send("sender", "room", "must-not-arrive");
  assert.deepEqual(received, []);

  const third = router.createDocument({
    instanceId: "third",
    displayUrl: "https://app.example.com/third",
    partition: identity,
    state: "active",
  });
  assert.ok(third.lifecycleId > after.lifecycleId);
  assert.ok(before.lifecycleId < after.lifecycleId);
});

test("paused callbacks are released in order and can pause themselves", async () => {
  const router = new BroadcastRouter();
  const identity = partition();
  activeDocument(router, "sender", "https://app.example.com/", identity);
  activeDocument(router, "receiver", "https://app.example.com/r", identity);

  const received: string[] = [];
  router.subscribe("receiver", "room", {
    id: "pausable",
    onMessage: (delivery) => {
      received.push(String(delivery.payload));
      if (delivery.payload === "one") {
        router.pauseReceiving("receiver", "pausable");
      }
    },
  });

  router.send("sender", "room", "one");
  router.send("sender", "room", "two");
  router.send("sender", "room", "three");
  assert.deepEqual(received, ["one"]);

  await router.resumeReceiving("receiver", "pausable");
  assert.deepEqual(received, ["one", "two", "three"]);
});

test("async receive callbacks are serialized per subscription", async () => {
  const router = new BroadcastRouter();
  const identity = partition();
  activeDocument(router, "sender", "https://app.example.com/", identity);
  activeDocument(router, "receiver", "https://app.example.com/r", identity);

  let releaseFirst: (() => void) | undefined;
  const firstGate = new Promise<void>((resolve) => {
    releaseFirst = resolve;
  });
  const events: string[] = [];
  router.subscribe("receiver", "room", {
    id: "async-sub",
    onMessage: async (delivery) => {
      events.push(`start:${delivery.payload}`);
      if (delivery.payload === "first") await firstGate;
      events.push(`end:${delivery.payload}`);
    },
  });

  router.send("sender", "room", "first");
  router.send("sender", "room", "second");
  assert.deepEqual(events, ["start:first"]);

  releaseFirst?.();
  await router.flush();
  assert.deepEqual(events, [
    "start:first",
    "end:first",
    "start:second",
    "end:second",
  ]);
});

test("revocation while paused discards queued deliveries", async () => {
  const router = new BroadcastRouter();
  const identity = partition();
  activeDocument(router, "sender", "https://app.example.com/", identity);
  activeDocument(router, "receiver", "https://app.example.com/r", identity);

  const received: unknown[] = [];
  router.subscribe("receiver", "room", {
    id: "revoked",
    paused: true,
    onMessage: (delivery) => received.push(delivery.payload),
  });

  router.send("sender", "room", "queued");
  const queued = router.getDeliveries();
  assert.equal(queued.length, 1);
  assert.equal(queued[0].status, "queued");

  router.unsubscribe("revoked");
  await router.resumeReceiving("receiver", "revoked");
  assert.deepEqual(received, []);
  const dropped = router.getDelivery(queued[0].deliveryId);
  assert.equal(dropped?.status, "dropped");
  assert.equal(
    router.getDecision(queued[0].deliveryId)?.reason,
    "subscription-revoked",
  );
});

test("entering bfcache invalidates already queued messages", async () => {
  const router = new BroadcastRouter();
  const identity = partition();
  activeDocument(router, "sender", "https://app.example.com/", identity);
  activeDocument(router, "receiver", "https://app.example.com/r", identity);

  const received: unknown[] = [];
  router.subscribe("receiver", "room", {
    id: "cache-sub",
    paused: true,
    onMessage: (delivery) => received.push(delivery.payload),
  });

  router.send("sender", "room", "staged-before-cache");
  router.enterBackForwardCache("receiver");
  router.restoreDocument("receiver");
  await router.resumeReceiving("receiver", "cache-sub");
  assert.deepEqual(received, []);

  router.send("sender", "room", "after-restore");
  assert.deepEqual(received, ["after-restore"]);
});

test("same channel name after bfcache restore gets a new sender generation", () => {
  const router = new BroadcastRouter();
  const identity = partition();
  activeDocument(router, "sender", "https://app.example.com/", identity);
  activeDocument(router, "receiver", "https://app.example.com/r", identity);

  const received: unknown[] = [];
  router.subscribe("receiver", "room", {
    id: "restored-sub",
    onMessage: (delivery) => received.push(delivery.payload),
  });

  const firstSession = router.openChannel("sender", "room");
  const oldEnvelope = router.send("sender", "room", "old-generation", {
    deliver: false,
  }).envelope;

  router.enterBackForwardCache("sender");
  router.enterBackForwardCache("receiver");
  router.restoreDocument("sender");
  router.restoreDocument("receiver");

  const secondSession = router.openChannel("sender", "room");
  assert.equal(firstSession.channelName, secondSession.channelName);
  assert.ok(secondSession.generation > firstSession.generation);

  router.send("sender", "room", "new-generation");
  assert.deepEqual(received, ["new-generation"]);

  const late = router.transmit(oldEnvelope);
  assert.equal(late.status, "rejected");
  assert.equal(late.reason, "stale-channel-generation");
  assert.deepEqual(received, ["new-generation"]);
});

test("late old sequence numbers and at-least-once duplicates are not redelivered", () => {
  const router = new BroadcastRouter();
  const identity = partition();
  activeDocument(router, "sender", "https://app.example.com/", identity);
  activeDocument(router, "receiver", "https://app.example.com/r", identity);

  const received: unknown[] = [];
  router.subscribe("receiver", "room", {
    id: "sequence-sub",
    onMessage: (delivery) => received.push(delivery.payload),
  });

  router.openChannel("sender", "room");
  const old = router.send("sender", "room", "old-sequence", {
    deliver: false,
  }).envelope;
  const newest = router.send("sender", "room", "newest");

  assert.equal(newest.envelope.sequence, 2);
  const late = router.transmit(old);
  assert.equal(late.status, "rejected");
  assert.equal(late.reason, "stale-sequence");

  const duplicate = router.transmit(newest.envelope);
  assert.equal(duplicate.status, "duplicate");
  assert.equal(duplicate.deliveries.length, 0);
  assert.deepEqual(received, ["newest"]);
  assert.equal(router.getDeliveries().length, 1);
});

test("close and ack racing retains one auditable decision", () => {
  const router = new BroadcastRouter();
  const identity = partition();
  activeDocument(router, "sender", "https://app.example.com/", identity);
  activeDocument(router, "receiver", "https://app.example.com/r", identity);
  router.subscribe("receiver", "room", { id: "decision-sub" });

  const sent = router.send("sender", "room", "payload");
  const delivery = router.getDeliveries()[0];
  assert.ok(delivery);

  const closed = router.closeDelivery(delivery.deliveryId, "close-ack-lost");
  assert.equal(closed.status, "closed");
  const ackAfterClose = router.confirmDelivery(
    delivery.deliveryId,
    "late-ack",
  );
  assert.equal(ackAfterClose.status, "closed");
  assert.equal(ackAfterClose.at, closed.at);

  const retry = router.closeDelivery(
    delivery.deliveryId,
    "retry-close-after-lost-ack",
  );
  assert.equal(retry.at, closed.at);

  const duplicate = router.transmit(sent.envelope);
  assert.equal(duplicate.status, "duplicate");
  assert.equal(router.getDeliveries().length, 1);

  const audit = router.getDecisionAudit(delivery.deliveryId);
  assert.ok(audit);
  assert.equal(audit.decision.status, "closed");
  assert.equal(audit.decision.by, "close");
  assert.equal(audit.attempts.filter((attempt) => attempt.accepted).length, 1);
  assert.deepEqual(
    audit.attempts.map((attempt) => [attempt.requested, attempt.accepted]),
    [
      ["close", true],
      ["ack", false],
      ["close", false],
    ],
  );
});

test("an ack that wins the race cannot later be changed by close", () => {
  const router = new BroadcastRouter();
  const identity = partition();
  activeDocument(router, "sender", "https://app.example.com/", identity);
  activeDocument(router, "receiver", "https://app.example.com/r", identity);
  router.subscribe("receiver", "room", { id: "ack-first-sub" });

  router.send("sender", "room", "payload");
  const delivery = router.getDeliveries()[0];
  assert.ok(delivery);

  const confirmed = router.confirmDelivery(delivery.deliveryId, "ack");
  const closed = router.closeDelivery(delivery.deliveryId, "late-close");
  assert.equal(confirmed.status, "confirmed");
  assert.equal(closed.status, "confirmed");
  assert.equal(closed.at, confirmed.at);
});
