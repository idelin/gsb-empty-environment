import assert from "node:assert/strict";
import { test } from "node:test";
import { IncrementalAuthorizationEngine } from "../src/index.ts";
import type { BatchItem, SignedConsentStatement } from "../src/types.ts";
import {
  aliasAssertion,
  correct,
  DAY,
  grant,
  keyDirectory,
  makeIssuer,
  purposes,
  recovery,
  regions,
  revoke,
  seededShuffle,
  T0,
} from "./fixtures.ts";

const SUBJECT = "subj:001";
const ADDRESS_A = "a@example.test";
const SENDER_X = "sender-x";
const SENDER_Y = "sender-y";

const aliasIssuer = makeIssuer("alias-key", "alias-chain");
const aliases = [
  aliasAssertion(aliasIssuer, "al-1", ADDRESS_A, SUBJECT, T0 - 86400000),
];

function item(
  itemId: string,
  sender = SENDER_X,
  region = "EU",
  purpose = "marketing",
  chainHint?: string,
): BatchItem {
  return { itemId, recipient: ADDRESS_A, purpose, sender, region, chainHint };
}

test("non-overlapping recovery restores a later window; overlap poisons the chain", () => {
  const issuer = makeIssuer("issuer-rec", "chain-rec");
  const keys = keyDirectory(issuer, aliasIssuer);
  const g1 = grant(issuer, {
    id: "g1",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + 10 * DAY,
  });
  const r1 = revoke(issuer, {
    id: "r1",
    subject: SUBJECT,
    targetGrantIds: ["g1"],
    filter: {},
    issuedAt: T0 + 2 * DAY,
  });
  const g2 = recovery(issuer, {
    id: "g2",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0 + 10 * DAY,
    until: T0 + 20 * DAY,
    supersedes: "g1",
  });
  const engine = new IncrementalAuthorizationEngine(keys, purposes, regions, {
    statements: [g1, r1, g2],
    aliases,
  });

  assert.equal(
    engine.evaluateBatch([item("gap")], T0 + 5 * DAY).deliverable.length,
    0,
  );
  assert.equal(
    engine.evaluateBatch([item("restored")], T0 + 12 * DAY).deliverable.length,
    1,
  );

  const badIssuer = makeIssuer("issuer-bad", "chain-bad");
  const badKeys = keyDirectory(badIssuer, aliasIssuer);
  const bg1 = grant(badIssuer, {
    id: "bg1",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + 10 * DAY,
  });
  const badRecovery = recovery(badIssuer, {
    id: "bg2",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0 + 5 * DAY,
    until: T0 + 15 * DAY,
    supersedes: "bg1",
  });
  const badEngine = new IncrementalAuthorizationEngine(
    badKeys,
    purposes,
    regions,
    { statements: [bg1, badRecovery], aliases },
  );
  const bad = badEngine.evaluateBatch([item("bad")], T0 + 12 * DAY);
  assert.equal(bad.pending[0]?.reason, "unverified-signature");
});

test("issuer fork makes the chain unusable and forces pending", () => {
  const issuer = makeIssuer("issuer-fork", "chain-fork");
  const keys = keyDirectory(issuer, aliasIssuer);
  const g1 = grant(issuer, {
    id: "g1",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + 10 * DAY,
  });
  const forked = grant(
    { key: issuer.key, chainId: issuer.chainId, seq: 1 },
    {
      id: "g1-fork",
      subject: SUBJECT,
      purpose: "marketing",
      senders: [SENDER_Y],
      regions: ["US"],
      from: T0,
      until: T0 + 10 * DAY,
      seq: 1,
    },
  );
  const engine = new IncrementalAuthorizationEngine(keys, purposes, regions, {
    statements: [g1, forked],
    aliases,
  });
  const evaluation = engine.evaluateBatch([item("fork")], T0 + DAY);
  assert.equal(evaluation.pending[0]?.reason, "forked-chain");
});

test("explicit correction replaces scope regardless of arrival order", () => {
  const issuer = makeIssuer("issuer-corr", "chain-corr");
  const keys = keyDirectory(issuer, aliasIssuer);
  const original = grant(issuer, {
    id: "g1",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + 10 * DAY,
  });
  const replacementEnvelope = grant(
    { key: issuer.key, chainId: issuer.chainId, seq: issuer.seq },
    {
      id: "g1",
      subject: SUBJECT,
      purpose: "marketing",
      senders: [SENDER_X],
      regions: ["US"],
      from: T0,
      until: T0 + 10 * DAY,
      issuedAt: T0 - 3600_000,
      seq: 1,
    },
  );
  const correction = correct(issuer, "c1", T0 + 2 * DAY, replacementEnvelope);

  for (const seed of [1, 7, 42]) {
    const shuffled = seededShuffle<SignedConsentStatement>(
      [original, correction],
      seed,
    );
    const engine = new IncrementalAuthorizationEngine(
      keys,
      purposes,
      regions,
      { statements: shuffled, aliases },
    );
    assert.equal(
      engine.evaluateBatch([item("eu", SENDER_X, "EU")], T0 + DAY)
        .deliverable.length,
      0,
    );
    assert.equal(
      engine.evaluateBatch([item("us", SENDER_X, "US")], T0 + DAY)
        .deliverable.length,
      1,
    );
  }
});

test("pinned chain hint cannot be satisfied by a different covering chain", () => {
  const issuerA = makeIssuer("issuer-pa", "chain-pa");
  const issuerB = makeIssuer("issuer-pb", "chain-pb");
  const keys = keyDirectory(issuerA, issuerB, aliasIssuer);
  const ga = grant(issuerA, {
    id: "ga",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + 10 * DAY,
  });
  const gb = grant(issuerB, {
    id: "gb",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + 10 * DAY,
  });
  const engine = new IncrementalAuthorizationEngine(keys, purposes, regions, {
    statements: [ga, gb],
    aliases,
  });

  const pinned = engine.evaluateBatch(
    [item("pinned", SENDER_X, "EU", "marketing", "chain-pa")],
    T0 + DAY,
  );
  assert.equal(pinned.deliverable[0]?.basis?.chainId, "chain-pa");

  const wrongPin = engine.evaluateBatch(
    [item("wrong", SENDER_Y, "EU", "marketing", "chain-pa")],
    T0 + DAY,
  );
  assert.equal(wrongPin.pending[0]?.reason, "pinned-chain-mismatch");
});
