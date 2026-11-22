import test from "node:test";
import assert from "node:assert/strict";

import {
  enumerateLegalOrders,
  verifyPartialOrder,
} from "../src/index.ts";
import type {
  PartialOrderEvent,
  PartitionInput,
} from "../src/index.ts";

const sharedPartition: PartitionInput = {
  topLevelRegistrableDomain: "example.com",
  initiatorOrigin: "https://app.example.com",
  crossSiteAncestor: false,
  configVersion: "v1",
};

function documentEvent(
  id: string,
  dependsOn: readonly string[] = [],
): PartialOrderEvent {
  return {
    id,
    dependsOn,
    action: {
      type: "createDocument",
      document: id,
      displayUrl: `https://app.example.com/${id}`,
      partition: sharedPartition,
      state: "active",
    },
  };
}

test("verifier distinguishes necessary deliveries from order-dependent ones", async () => {
  const events: PartialOrderEvent[] = [
    documentEvent("sender"),
    documentEvent("always-page", ["sender"]),
    documentEvent("maybe-page", ["always-page"]),
    {
      id: "always-sub",
      dependsOn: ["always-page"],
      action: {
        type: "subscribe",
        subscription: "always-sub",
        document: "always-page",
        channel: "room",
      },
    },
    {
      id: "maybe-sub",
      dependsOn: ["maybe-page"],
      action: {
        type: "subscribe",
        subscription: "maybe-sub",
        document: "maybe-page",
        channel: "room",
      },
    },
    {
      id: "send",
      dependsOn: ["sender", "maybe-page", "always-sub"],
      action: {
        type: "send",
        messageId: "message-1",
        document: "sender",
        channel: "room",
        payload: "hello",
      },
    },
  ];

  const report = await verifyPartialOrder(events);
  assert.ok(report.orderCount >= 2);

  assert.deepEqual(
    report.always.map((delivery) => delivery.subscriptionId),
    ["always-sub"],
  );
  assert.deepEqual(
    report.orderDependent.map((delivery) => delivery.subscriptionId),
    ["maybe-sub"],
  );
  assert.equal(report.must.length, 1);
  assert.equal(report.may.length, 1);

  const always = report.always[0];
  assert.equal(always.messageId, "message-1");
  assert.equal(always.recipientDocument, "always-page");
  assert.equal(always.occurrenceCount, report.orderCount);
  assert.equal(always.classification, "always");

  const maybe = report.orderDependent[0];
  assert.equal(maybe.recipientDocument, "maybe-page");
  assert.ok(maybe.occurrenceCount > 0);
  assert.ok(maybe.occurrenceCount < report.orderCount);
  assert.equal(maybe.classification, "order-dependent");
});

test("pause racing a send is reported as order dependent", async () => {
  const events: PartialOrderEvent[] = [
    documentEvent("sender"),
    documentEvent("receiver", ["sender"]),
    {
      id: "sub",
      dependsOn: ["receiver"],
      action: {
        type: "subscribe",
        subscription: "sub",
        document: "receiver",
        channel: "room",
      },
    },
    {
      id: "pause",
      dependsOn: ["receiver", "sub"],
      action: { type: "pause", document: "receiver", subscription: "sub" },
    },
    {
      id: "send",
      dependsOn: ["sender", "sub"],
      action: {
        type: "send",
        messageId: "message-race",
        document: "sender",
        channel: "room",
        payload: "race",
      },
    },
  ];

  const report = await verifyPartialOrder(events);
  assert.equal(report.orderCount, 2);
  assert.equal(report.always.length, 0);
  assert.equal(report.orderDependent.length, 1);
  assert.equal(report.orderDependent[0].messageId, "message-race");
});

test("enumerateLegalOrders rejects dependency cycles", () => {
  assert.throws(
    () =>
      enumerateLegalOrders([
        {
          id: "a",
          dependsOn: ["b"],
          action: { type: "pause", document: "a" },
        },
        {
          id: "b",
          dependsOn: ["a"],
          action: { type: "pause", document: "b" },
        },
      ]),
    /cycle/,
  );
});
