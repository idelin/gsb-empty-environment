import assert from "node:assert/strict";
import { test } from "node:test";
import { IncrementalAuthorizationEngine } from "../src/index.ts";
import type { BatchItem, SignedAliasAssertion } from "../src/types.ts";
import {
  aliasAssertion,
  DAY,
  grant,
  keyDirectory,
  makeIssuer,
  purposes,
  regions,
  T0,
} from "./fixtures.ts";

const SUBJECT = "subj:001";
const ADDRESS_A = "a@example.test";
const ADDRESS_B = "b@example.test";
const SENDER_X = "sender-x";
const SENDER_Y = "sender-y";

function item(itemId: string, recipient: string, sender: string): BatchItem {
  return { itemId, recipient, purpose: "marketing", sender, region: "EU" };
}

test("multiple aliases map to one subject and share its consent", () => {
  const issuer = makeIssuer("issuer-alias", "chain-alias");
  const keys = keyDirectory(issuer);
  const g1 = grant(issuer, {
    id: "g1",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + 10 * DAY,
  });
  const aliases: SignedAliasAssertion[] = [
    aliasAssertion(issuer, "al-1", ADDRESS_A, SUBJECT, T0 - DAY),
    aliasAssertion(issuer, "al-2", ADDRESS_B, SUBJECT, T0 - DAY),
  ];
  const engine = new IncrementalAuthorizationEngine(keys, purposes, regions, {
    statements: [g1],
    aliases,
  });

  const both = engine.evaluateBatch(
    [
      item("via-a", ADDRESS_A, SENDER_X),
      item("via-b", ADDRESS_B, SENDER_X),
      item("unknown", "nobody@example.test", SENDER_X),
    ],
    T0 + DAY,
  );
  assert.equal(both.deliverable.length, 2);
  assert.equal(both.pending[0]?.reason, "unknown-recipient");

  engine.ingestAliases([
    aliasAssertion(issuer, "al-3", ADDRESS_A, "subj:OTHER", T0 + 1000),
  ]);
  const ambiguous = engine.evaluateBatch(
    [item("amb", ADDRESS_A, SENDER_X)],
    T0 + DAY,
  );
  assert.equal(ambiguous.pending[0]?.reason, "ambiguous-recipient");
});

test("receipts disclose only each sender's own results", () => {
  const issuer = makeIssuer("issuer-receipt", "chain-receipt");
  const keys = keyDirectory(issuer);
  const g1 = grant(issuer, {
    id: "g1",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + 10 * DAY,
  });
  const engine = new IncrementalAuthorizationEngine(keys, purposes, regions, {
    statements: [g1],
    aliases: [aliasAssertion(issuer, "al-1", ADDRESS_A, SUBJECT, T0 - DAY)],
  });
  const items = [
    item("x-ok", ADDRESS_A, SENDER_X),
    item("y-no-consent", ADDRESS_A, SENDER_Y),
  ];
  const receipts = engine.senderReceipts(items, T0 + DAY);

  assert.equal(receipts.length, 2);
  const xReceipt = receipts.find((receipt) => receipt.sender === SENDER_X);
  const yReceipt = receipts.find((receipt) => receipt.sender === SENDER_Y);
  assert.deepEqual(xReceipt?.results.map((d) => d.itemId), ["x-ok"]);
  assert.deepEqual(yReceipt?.results.map((d) => d.itemId), ["y-no-consent"]);
  assert.equal(
    yReceipt?.results.every((decision) => decision.basis === undefined),
    true,
  );
});
