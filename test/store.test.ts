import { test } from "node:test";
import assert from "node:assert/strict";
import { InMemoryCasStore, withCasRetry } from "../src/index.ts";

test("CAS rejects stale expected versions", async () => {
  const store = new InMemoryCasStore();
  const created = await store.cas("k", null, { count: 1 });
  assert.ok(created);
  const stale = await store.cas("k", "wrong-version", { count: 2 });
  assert.equal(stale, null);
  const current = await store.get<{ count: number }>("k");
  assert.equal(current?.value.count, 1);
});

test("CAS retry converges under concurrent writers", async () => {
  const store = new InMemoryCasStore();
  await store.cas<{ count: number }>("counter", null, { count: 0 });
  await Promise.all(
    Array.from({ length: 20 }, () =>
      withCasRetry(store, "counter", (current: { count: number } | null) => ({
        count: (current?.count ?? 0) + 1,
      })),
    ),
  );
  const finalRecord = await store.get<{ count: number }>("counter");
  assert.equal(finalRecord?.value.count, 20);
});

test("every durable coordinator operation stays on a single session key", async () => {
  const { makeHarness, invite, consent, answer } = await import("./helpers.ts");
  const h = makeHarness();
  const invitation = invite(h, "s-keys", "g1");
  await h.nodeA.submitFact(
    "s-keys",
    h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }),
  );
  await h.nodeA.submitFact("s-keys", consent(h, invitation, h.alice));
  await h.nodeA.submitFact("s-keys", consent(h, invitation, h.bob));
  await h.nodeA.registerConnection("s-keys", h.bob.identity, "bob", "addr", 1);
  await h.nodeA.submitFact("s-keys", answer(h, invitation, "a1", h.alice, h.bob));

  const sessionRecord = await h.store.get<{ events: unknown[] }>("session:s-keys");
  assert.ok(sessionRecord);
  assert.equal((sessionRecord?.value.events.length ?? 0) >= 5, true);
});

test("concurrent fact submissions serialize through CAS without losing events", async () => {
  const { makeHarness, invite, consent } = await import("./helpers.ts");
  const { signAnswer, digestOf } = await import("../src/index.ts");
  const h = makeHarness();
  const invitation = invite(h, "s-cas-contention", "g1");
  await h.nodeA.submitFact(
    "s-cas-contention",
    h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }),
  );
  await h.nodeA.submitFact("s-cas-contention", consent(h, invitation, h.alice));
  await h.nodeA.submitFact("s-cas-contention", consent(h, invitation, h.bob));

  const answers = Array.from({ length: 10 }, (_, index) => {
    return signAnswer(
      h.sig,
      h.alice,
      {
        answerId: `parallel-${index}`,
        sessionId: "s-cas-contention",
        invitationDigest: digestOf(invitation),
        group: "g1",
        from: h.alice.identity,
        to: h.bob.identity,
        body: `body-${index}`,
        createdAt: h.clock.now,
      },
      h.clock.now,
    );
  });

  const results = await Promise.all(
    answers.map((fact) => h.nodeA.submitFact("s-cas-contention", fact)),
  );
  const accepted = results.filter((result) => result.ok);
  assert.equal(accepted.length, 10);
  const record = await h.store.get<{ events: unknown[] }>("session:s-cas-contention");
  assert.equal(record?.value.events.length, 13);
});
