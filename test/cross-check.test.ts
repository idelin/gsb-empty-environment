import assert from "node:assert/strict";
import { test } from "node:test";
import {
  IncrementalAuthorizationEngine,
  ReferenceAuthorizationEngine,
} from "../src/index.ts";
import type { BatchItem, SignedConsentStatement } from "../src/types.ts";
import {
  aliasAssertion,
  DAY,
  grant,
  keyDirectory,
  makeIssuer,
  purposes,
  regions,
  revoke,
  seededShuffle,
  T0,
} from "./fixtures.ts";

const SUBJECT = "subj:001";
const ADDRESS_A = "a@example.test";
const SENDER_X = "sender-x";
const SENDER_Y = "sender-y";

function item(
  itemId: string,
  purpose: string,
  sender: string,
  region: string,
): BatchItem {
  return { itemId, recipient: ADDRESS_A, purpose, sender, region };
}

test("reference oracle and incremental view agree across instants and orderings", () => {
  const issuer = makeIssuer("issuer-cross", "chain-cross");
  const keys = keyDirectory(issuer);
  const g1 = grant(issuer, {
    id: "g1",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X, SENDER_Y],
    regions: ["EU", "US"],
    from: T0,
    until: T0 + 20 * DAY,
  });
  const g2 = grant(issuer, {
    id: "g2",
    subject: SUBJECT,
    purpose: "marketing.promo",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0 + 2 * DAY,
    until: T0 + 18 * DAY,
  });
  const r1 = revoke(issuer, {
    id: "r1",
    subject: SUBJECT,
    targetGrantIds: ["g2"],
    filter: { senders: [SENDER_X] },
    issuedAt: T0 + 4 * DAY,
  });
  const r2 = revoke(issuer, {
    id: "r2",
    subject: SUBJECT,
    targetGrantIds: ["g1"],
    filter: {
      purposes: ["marketing.promo"],
      regions: ["US"],
      interval: { from: T0 + 6 * DAY, to: T0 + 9 * DAY },
    },
    issuedAt: T0 + 5 * DAY,
  });
  const statements: SignedConsentStatement[] = [g1, g2, r1, r2];
  const aliases = [aliasAssertion(issuer, "al-1", ADDRESS_A, SUBJECT, T0 - DAY)];

  const items: BatchItem[] = [
    item("promo-eu-x", "marketing.promo", SENDER_X, "EU"),
    item("promo-us-x", "marketing.promo", SENDER_X, "US"),
    item("promo-eu-y", "marketing.promo", SENDER_Y, "EU"),
    item("news-eu-x", "marketing.newsletter", SENDER_X, "EU"),
    item("news-apac-x", "marketing.newsletter", SENDER_X, "APAC"),
  ];
  const instants = [
    T0 - 1,
    T0,
    T0 + DAY,
    T0 + 3 * DAY,
    T0 + 5 * DAY,
    T0 + 7 * DAY,
    T0 + 9 * DAY,
    T0 + 10 * DAY,
    T0 + 19 * DAY,
    T0 + 20 * DAY,
  ];

  for (const seed of [1, 2, 3, 13, 99]) {
    const shuffled = seededShuffle(statements, seed);
    const reference = new ReferenceAuthorizationEngine(
      { statements: shuffled, aliases },
      keys,
      purposes,
      regions,
    );
    const incremental = new IncrementalAuthorizationEngine(
      keys,
      purposes,
      regions,
      { statements: shuffled, aliases },
    );
    for (const at of instants) {
      const expected = reference.evaluateBatch(items, at);
      const actual = incremental.evaluateBatch(items, at);
      const simplify = (all: typeof actual.all) =>
        all.map(({ itemId, status, reason }) => ({ itemId, status, reason }));
      assert.deepEqual(
        simplify(actual.all),
        simplify(expected.all),
        `seed=${seed} at=${at}`,
      );
      assert.equal(
        actual.deliverable.length,
        expected.deliverable.length,
        `maximal subset size seed=${seed} at=${at}`,
      );
    }
  }
});

test("a selective revocation only invalidates its own chain in the cache", () => {
  const issuerA = makeIssuer("issuer-ca", "chain-ca");
  const issuerB = makeIssuer("issuer-cb", "chain-cb");
  const keys = keyDirectory(issuerA, issuerB);
  const ga = grant(issuerA, {
    id: "ga",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + 20 * DAY,
  });
  const gb = grant(issuerB, {
    id: "gb",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + 20 * DAY,
  });
  const engine = new IncrementalAuthorizationEngine(keys, purposes, regions, {
    statements: [ga, gb],
    aliases: [aliasAssertion(issuerA, "al-1", ADDRESS_A, SUBJECT, T0 - DAY)],
  });
  engine.evaluateBatch([item("warm", "marketing", SENDER_X, "EU")], T0 + DAY);
  const warmStats = engine.cacheStats();
  assert.equal(warmStats.chainRematerializations, 2);

  // Re-reading without changes hits the cache entirely.
  engine.evaluateBatch([item("warm2", "marketing", SENDER_X, "EU")], T0 + DAY);
  assert.equal(engine.cacheStats().chainRematerializations, 2);

  // A revocation on chain A invalidates only chain A.
  engine.ingestStatements([
    revoke(issuerA, {
      id: "ra",
      subject: SUBJECT,
      targetGrantIds: ["ga"],
      filter: {},
      issuedAt: T0 + 2 * DAY,
    }),
  ]);
  engine.evaluateBatch([item("after", "marketing", SENDER_X, "EU")], T0 + 3 * DAY);
  const stats = engine.cacheStats();
  // Constructor ingestion plus the explicit revocation ingestion.
  assert.equal(stats.ingestCalls, warmStats.ingestCalls + 1);
  // Only chain A was invalidated; chain B keeps being served from cache.
  assert.equal(stats.chainRematerializations - warmStats.chainRematerializations, 1);

  // The cached view now reflects the selective revocation on chain A while
  // chain B's independent grant still keeps the item deliverable.
  const evaluation = engine.evaluateBatch(
    [item("still-ok", "marketing", SENDER_X, "EU")],
    T0 + 4 * DAY,
  );
  assert.equal(evaluation.deliverable.length, 1);
  assert.equal(evaluation.deliverable[0]?.basis?.chainId, "chain-cb");

  // The full-rebuild snapshot matches the incrementally maintained cache.
  const rebuilt = engine.fullRebuildReference();
  const snapshot = engine.snapshot();
  assert.equal(snapshot.length, rebuilt.length);
  assert.deepEqual(
    snapshot.map((chain) => [chain.chainId, chain.cells.length] as const),
    rebuilt.map((chain) => [chain.chainId, chain.cells.length] as const),
  );
});

test("tampered signature is rejected and the decision stays pending", () => {
  const issuer = makeIssuer("issuer-tamper", "chain-tamper");
  const aliasIssuer = makeIssuer("alias-tamper", "alias-tamper-chain");
  const keys = keyDirectory(issuer, aliasIssuer);
  const envelope = grant(issuer, {
    id: "g1",
    subject: SUBJECT,
    purpose: "marketing",
    senders: [SENDER_X],
    regions: ["EU"],
    from: T0,
    until: T0 + 10 * DAY,
  });
  const tampered: SignedConsentStatement = {
    statement: envelope.statement,
    signature: `${envelope.signature.slice(0, -2)}AA`,
  };
  const engine = new IncrementalAuthorizationEngine(keys, purposes, regions, {
    statements: [tampered],
    aliases: [aliasAssertion(aliasIssuer, "al-1", ADDRESS_A, SUBJECT, T0 - DAY)],
  });
  const evaluation = engine.evaluateBatch(
    [item("tampered", "marketing", SENDER_X, "EU")],
    T0 + DAY,
  );
  assert.equal(evaluation.pending[0]?.reason, "unverified-signature");
});
