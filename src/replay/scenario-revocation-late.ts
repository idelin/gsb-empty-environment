import { signAnswer, signInvitation, signRevocation } from "../index.ts";
import { World, type ScenarioReport, type ScenarioCheck } from "./world.ts";

/**
 * Scenario 3: revocation followed by late confirmation.
 * A revocation signed by the initiator and bound to the invitation digest
 * closes the session; consent and answer facts that arrive afterwards are
 * rejected instead of silently reviving the session.
 */
export async function scenarioRevocationLate(w: World): Promise<ScenarioReport> {
  const sessionId = "revoke-1";
  const invitation = w.invitation(sessionId, "group-green", w.alice, w.bob);
  const checks: ScenarioCheck[] = [];

  await w.submit(sessionId, signInvitation(w.sig, w.alice, invitation, w.tick()));
  await w.submit(sessionId, w.consent(invitation, w.alice));
  await w.submit(
    sessionId,
    signRevocation(
      w.sig,
      w.alice,
      sessionId,
      w.digest(invitation),
      "changed mind",
      w.tick(),
    ),
  );

  const lateConsent = await w.submit(sessionId, w.consent(invitation, w.bob));
  checks.push(
    w.check(
      "late consent after revocation is rejected",
      !lateConsent.ok && lateConsent.reason === "invitation-revoked",
      JSON.stringify(lateConsent),
    ),
  );

  const lateAnswer = signAnswer(
    w.sig,
    w.alice,
    {
      answerId: "late-1",
      sessionId,
      invitationDigest: w.digest(invitation),
      group: invitation.group,
      from: w.alice.identity,
      to: w.bob.identity,
      body: "too late",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  const answerResult = await w.submit(sessionId, lateAnswer);
  checks.push(
    w.check(
      "late answer after revocation is rejected",
      !answerResult.ok && answerResult.reason === "invitation-revoked",
      JSON.stringify(answerResult),
    ),
  );

  const state = await w.nodeA.getState(sessionId);
  checks.push(
    w.check(
      "session stays revoked with no queued answers",
      state.status === "revoked" && Object.keys(state.answers).length === 0,
      "revoked",
    ),
  );

  const replayed = await w.replayOnB(sessionId);
  checks.push(
    w.check(
      "revocation replays identically on node-B",
      JSON.stringify(replayed) === JSON.stringify(state),
      "replay equality",
    ),
  );

  return { scenario: "revocation-then-late-confirmation", checks, events: await w.events(sessionId) };
}
