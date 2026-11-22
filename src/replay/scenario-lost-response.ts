import { signAnswer, signInvitation } from "../index.ts";
import { World, type ScenarioReport, type ScenarioCheck } from "./world.ts";

/**
 * Scenario 4: the receiver already persisted a receipt while the relay lost
 * the confirmation response.
 *
 * Recovery must distinguish:
 *  - delivered and confirmed locally;
 *  - receiver-side receipt exists while the relay still thinks "in-flight"
 *    (reconciled, not redelivered, never re-routed);
 *  - no receipt at all, which is genuinely indeterminate.
 */
export async function scenarioLostResponse(w: World): Promise<ScenarioReport> {
  const sessionId = "lost-response-1";
  const invitation = w.invitation(sessionId, "group-yellow", w.alice, w.bob);
  const checks: ScenarioCheck[] = [];

  await w.submit(sessionId, signInvitation(w.sig, w.alice, invitation, w.tick()));
  await w.submit(sessionId, w.consent(invitation, w.alice));
  await w.submit(sessionId, w.consent(invitation, w.bob));
  await w.nodeA.registerConnection(sessionId, w.bob.identity, "bob", "net-1", 1);

  const answer = signAnswer(
    w.sig,
    w.alice,
    {
      answerId: "lost-1",
      sessionId,
      invitationDigest: w.digest(invitation),
      group: invitation.group,
      from: w.alice.identity,
      to: w.bob.identity,
      body: "important",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  await w.submit(sessionId, answer);

  const firstTry = await w.nodeA.attemptDelivery(sessionId, "lost-1");
  checks.push(
    w.check(
      "delivery attempt claims an idempotent receiver receipt",
      firstTry.status === "in-flight",
      JSON.stringify(firstTry),
    ),
  );

  // Repeated attempt is idempotent: the same receipt is claimed again.
  const secondTry = await w.nodeA.attemptDelivery(sessionId, "lost-1");
  checks.push(
    w.check(
      "retry stays in-flight against the same target and never duplicates",
      secondTry.status === "in-flight",
      JSON.stringify(secondTry),
    ),
  );

  // The relay "loses" the response, then the receiver confirms out of band.
  await w.nodeA.confirmDelivery(
    sessionId,
    "lost-1",
    (await w.receipts.getByAnswer("lost-1"))!.receiptId,
  );
  const confirmed = await w.nodeA.queryVerdict(sessionId, "lost-1");
  checks.push(
    w.check(
      "confirmation marks the answer delivered with its receipt id",
      confirmed.status === "delivered" &&
        confirmed.receiptId === (await w.receipts.getByAnswer("lost-1"))!.receiptId,
      JSON.stringify(confirmed),
    ),
  );

  // Simulate a second answer where the relay crashes after the receiver receipt
  // was claimed but before any delivery-confirm event was persisted.
  const answerTwo = signAnswer(
    w.sig,
    w.alice,
    {
      answerId: "lost-2",
      sessionId,
      invitationDigest: w.digest(invitation),
      group: invitation.group,
      from: w.alice.identity,
      to: w.bob.identity,
      body: "also important",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  await w.submit(sessionId, answerTwo);
  await w.nodeA.attemptDelivery(sessionId, "lost-2");

  const recoveredConfirmed = await w.nodeA.recoverAnswer(sessionId, "lost-1");
  checks.push(
    w.check(
      "confirmed answer recovers as delivered",
      recoveredConfirmed.kind === "delivered",
      JSON.stringify(recoveredConfirmed),
    ),
  );

  const recoveredInFlight = await w.nodeA.recoverAnswer(sessionId, "lost-2");
  checks.push(
    w.check(
      "in-flight answer with a receiver receipt is reconciled as delivered",
      recoveredInFlight.kind === "delivered-reconciled",
      JSON.stringify(recoveredInFlight),
    ),
  );

  // A third answer is never sent: recovery must report no determined result.
  const answerThree = signAnswer(
    w.sig,
    w.alice,
    {
      answerId: "lost-3",
      sessionId,
      invitationDigest: w.digest(invitation),
      group: invitation.group,
      from: w.alice.identity,
      to: w.bob.identity,
      body: "queued only",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  await w.submit(sessionId, answerThree);
  const recoveredQueued = await w.nodeA.recoverAnswer(sessionId, "lost-3");
  checks.push(
    w.check(
      "never-attempted answer is reported as not-attempted",
      recoveredQueued.kind === "not-attempted",
      JSON.stringify(recoveredQueued),
    ),
  );

  // Indeterminate case: in-flight in the log but receipt directory was lost.
  const answerFour = signAnswer(
    w.sig,
    w.alice,
    {
      answerId: "lost-4",
      sessionId,
      invitationDigest: w.digest(invitation),
      group: invitation.group,
      from: w.alice.identity,
      to: w.bob.identity,
      body: "uncertain",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  await w.submit(sessionId, answerFour);
  await w.nodeA.attemptDelivery(sessionId, "lost-4");
  const orphaned = await w.recoverWithEmptyDirectory(sessionId, "lost-4");
  checks.push(
    w.check(
      "in-flight without any receipt is indeterminate, never silently re-routed",
      orphaned.kind === "indeterminate",
      JSON.stringify(orphaned),
    ),
  );

  const replayed = await w.replayOnB(sessionId);
  const live = await w.nodeA.getState(sessionId);
  checks.push(
    w.check(
      "delivery events replay identically on node-B",
      JSON.stringify(replayed) === JSON.stringify(live),
      "replay equality",
    ),
  );

  return { scenario: "receiver-received-relay-lost-response", checks, events: await w.events(sessionId) };
}
