import assert from "node:assert/strict";
import { test } from "node:test";
import {
  IncrementalAuthorizationEngine,
  verifyBasisSignatures,
} from "../src/index.ts";
import type {
  BatchItem,
  SignedConsentStatement,
} from "../src/types.ts";
import {
  aliasAssertion,
  DAY,
  grant,
  HOUR,
  keyDirectory,
  makeIssuer,
  purposes,
  regions,
  revoke,
  T0,
} from "./fixtures.ts";

const SUBJECT = "subj:001";
const ADDRESS_A = "a@example.test";
const SENDER_X = "sender-x";
const SENDER_Y = "sender-y";
const aliasIssuer = makeIssuer("alias-key-authz", "alias-chain-authz");
const aliases = [
  aliasAssertion(aliasIssuer, "al-1", ADDRESS_A, SUBJECT, T0 - 86400000),
];

function item(
  itemId: string,
  recipient: string,
  purpose: string,
  sender: string,
  region: string,
): BatchItem {
  return { itemId, recipient, purpose, sender, region };
}

test("selective revocation cuts one dimension without revoking the whole grant", () => {
  const issuer = makeIssuer("issuer-1", "chain-1");
  const keys = keyDirectory(issuer, aliasIssuer);
  const g1 = grant(issuer, {
    id: "g1",
    subject: SUBJECT,
    purpose: "marketing.newsletter",
    senders: [SENDER_X, SENDER_Y],
    regions: ["EU", "US"],
    from: T0,
    until: T0 + 30 * DAY,
  });
  const r1 = revoke(issuer, {
    id: "r1",
    subject: SUBJECT,
    targetGrantIds: ["g1"],
    filter: {
      purposes: ["marketing.newsletter.weekly"],
      senders: [SENDER_X],
      regions: ["EU"],
    },
    issuedAt: T0 + DAY,
  });

  const engine = new IncrementalAuthorizationEngine(keys, purposes, regions, {
    statements: [g1, r1],
    aliases,
  });
  const at = T0 + 2 * DAY;

  const cutEvaluation = engine.evaluateBatch(
    [item("cut", ADDRESS_A, "marketing.newsletter.weekly", SENDER_X, "EU")],
    at,
  );
  assert.equal(cutEvaluation.deliverable.length, 0);
  assert.equal(cutEvaluation.denied[0]?.reason, "no-consent");

  const survivors = engine.evaluateBatch(
    [
      item("daily", ADDRESS_A, "marketing.newsletter.daily", SENDER_X, "EU"),
      item("weekly-y", ADDRESS_A, "marketing.newsletter.weekly", SENDER_Y, "EU"),
      item("weekly-us", ADDRESS_A, "marketing.newsletter.weekly", SENDER_X, "US"),
      item("parent", ADDRESS_A, "marketing.newsletter", SENDER_X, "EU"),
    ],
    at,
  );
  assert.deepEqual(
    survivors.all.map((decision) => decision.status),
    ["deliverable", "deliverable", "deliverable", "deliverable"],
  );
});

test("two overlapping grants: revoking one leaves the other lawful", () => {
  const issuer = makeIssuer("issuer-2", "chain-2");
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
  const g2 = grant(issuer, {
    id: "g2",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0 + 5 * DAY,
    until: T0 + 20 * DAY,
  });
  const revokeFirst = revoke(issuer, {
    id: "r-only-g1",
    subject: SUBJECT,
    targetGrantIds: ["g1"],
    filter: {},
    issuedAt: T0 + 6 * DAY,
  });

  const engine = new IncrementalAuthorizationEngine(keys, purposes, regions, {
    statements: [g1, g2, revokeFirst],
    aliases,
  });

  const overlap = engine.evaluateBatch(
    [item("overlap", ADDRESS_A, "marketing.promo", SENDER_X, "EU")],
    T0 + 7 * DAY,
  );
  assert.equal(overlap.deliverable.length, 1);
  const basis = overlap.deliverable[0]?.basis;
  assert.ok(basis);
  assert.deepEqual(
    basis?.grants.map((evidence) => evidence.statementId),
    ["g2"],
  );
  assert.deepEqual(basis?.revocations, []);

  const before = engine.evaluateBatch(
    [item("before", ADDRESS_A, "marketing.promo", SENDER_X, "EU")],
    T0 + DAY,
  );
  assert.equal(before.deliverable.length, 0);

  const allStatements: SignedConsentStatement[] = [g1, g2, revokeFirst];
  assert.equal(
    verifyBasisSignatures(basis as NonNullable<typeof basis>, allStatements, keys),
    true,
  );
});

test("same address, different chains: purpose/region cannot be stitched together", () => {
  const issuerA = makeIssuer("issuer-a", "chain-a");
  const issuerB = makeIssuer("issuer-b", "chain-b");
  const keys = keyDirectory(issuerA, issuerB, aliasIssuer);

  const ga = grant(issuerA, {
    id: "ga",
    subject: SUBJECT,
    purpose: "marketing.promo",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + 10 * DAY,
  });
  const gb = grant(issuerB, {
    id: "gb",
    subject: SUBJECT,
    purpose: "transactional.receipt",
    senders: [SENDER_X],
    regions: ["US"],
    from: T0,
    until: T0 + 10 * DAY,
  });

  const engine = new IncrementalAuthorizationEngine(keys, purposes, regions, {
    statements: [ga, gb],
    aliases,
  });
  const at = T0 + DAY;

  const stitched = engine.evaluateBatch(
    [item("stitch", ADDRESS_A, "marketing.promo", SENDER_X, "US")],
    at,
  );
  assert.equal(stitched.deliverable.length, 0);
  assert.equal(stitched.denied[0]?.reason, "no-consent");

  const fine = engine.evaluateBatch(
    [
      item("eu-promo", ADDRESS_A, "marketing.promo", SENDER_X, "EU"),
      item("us-tx", ADDRESS_A, "transactional.receipt", SENDER_X, "US"),
    ],
    at,
  );
  assert.equal(fine.deliverable.length, 2);
});

test("half-open validity: boundary instant is excluded, lapsed windows denied", () => {
  const issuer = makeIssuer("issuer-window", "chain-window");
  const keys = keyDirectory(issuer, aliasIssuer);
  const g1 = grant(issuer, {
    id: "g1",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + DAY,
  });
  const engine = new IncrementalAuthorizationEngine(keys, purposes, regions, {
    statements: [g1],
    aliases,
  });

  assert.equal(
    engine.evaluateBatch([item("start", ADDRESS_A, "marketing", SENDER_X, "EU")], T0)
      .deliverable.length,
    1,
  );
  const atEnd = engine.evaluateBatch(
    [item("end", ADDRESS_A, "marketing", SENDER_X, "EU")],
    T0 + DAY,
  );
  assert.equal(atEnd.deliverable.length, 0);
  assert.equal(atEnd.denied[0]?.reason, "no-consent");
});

test("time-windowed revocation punches only a hole in the interval", () => {
  const issuer = makeIssuer("issuer-hole", "chain-hole");
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
    filter: { interval: { from: T0 + 3 * DAY, to: T0 + 5 * DAY } },
    issuedAt: T0 + 2 * DAY,
  });
  const engine = new IncrementalAuthorizationEngine(keys, purposes, regions, {
    statements: [g1, r1],
    aliases,
  });

  for (const [at, expected] of [
    [T0 + DAY, "deliverable"],
    [T0 + 3 * DAY, "denied"],
    [T0 + 4 * DAY + HOUR, "denied"],
    [T0 + 5 * DAY, "deliverable"],
    [T0 + 6 * DAY, "deliverable"],
  ] as const) {
    const evaluation = engine.evaluateBatch(
      [item("at", ADDRESS_A, "marketing", SENDER_X, "EU")],
      at,
    );
    assert.equal(evaluation.all[0]?.status, expected, `at=${at}`);
  }
});
