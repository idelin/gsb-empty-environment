import { signAnswer, signInvitation } from "../index.ts";
import { World, type ScenarioReport, type ScenarioCheck } from "./world.ts";

/**
 * Scenario 5: the same participant uses two content groups at once.
 * Consent is group-scoped and invitation-scoped, so a valid answer in one
 * group never becomes deliverable in the other session, and reusing a session
 * id under a different invitation is rejected rather than inheriting consent.
 */
export async function scenarioTwoGroups(w: World): Promise<ScenarioReport> {
  const checks: ScenarioCheck[] = [];
  const blue = w.invitation("two-group-blue", "group-blue", w.alice, w.bob);
  const red = w.invitation("two-group-red", "group-red", w.alice, w.bob);

  for (const invitation of [blue, red]) {
    await w.submit(invitation.sessionId, signInvitation(w.sig, w.alice, invitation, w.tick()));
    await w.submit(invitation.sessionId, w.consent(invitation, w.alice));
    await w.submit(invitation.sessionId, w.consent(invitation, w.bob));
    await w.nodeA.registerConnection(
      invitation.sessionId,
      w.bob.identity,
      "bob-shared-display",
      "shared-addr",
      1,
    );
  }

  const blueAnswer = signAnswer(
    w.sig,
    w.alice,
    {
      answerId: "blue-1",
      sessionId: blue.sessionId,
      invitationDigest: w.digest(blue),
      group: "group-blue",
      from: w.alice.identity,
      to: w.bob.identity,
      body: "blue content",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  await w.submit(blue.sessionId, blueAnswer);

  const redAnswer = signAnswer(
    w.sig,
    w.alice,
    {
      answerId: "red-1",
      sessionId: red.sessionId,
      invitationDigest: w.digest(red),
      group: "group-red",
      from: w.alice.identity,
      to: w.bob.identity,
      body: "red content",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  await w.submit(red.sessionId, redAnswer);

  const blueVerdict = await w.nodeA.queryVerdict(blue.sessionId, "blue-1");
  const redVerdict = await w.nodeA.queryVerdict(red.sessionId, "red-1");
  checks.push(
    w.check(
      "each group routes only its own answer",
      blueVerdict.status === "deliverable" && redVerdict.status === "deliverable",
      `${JSON.stringify(blueVerdict)} | ${JSON.stringify(redVerdict)}`,
    ),
  );

  const crossGroup = signAnswer(
    w.sig,
    w.alice,
    {
      answerId: "cross-1",
      sessionId: red.sessionId,
      invitationDigest: w.digest(red),
      group: "group-blue",
      from: w.alice.identity,
      to: w.bob.identity,
      body: "blue content pushed into red session",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  const rejected = await w.submit(red.sessionId, crossGroup);
  checks.push(
    w.check(
      "an answer from another content group is rejected",
      !rejected.ok && rejected.reason === "group-mismatch",
      JSON.stringify(rejected),
    ),
  );

  // A brand-new invitation reusing an existing session id cannot inherit the
  // consent already stored under that id.
  const reused = w.invitation(blue.sessionId, "group-blue", w.alice, w.bob);
  const reusedInvite = signInvitation(w.sig, w.alice, reused, w.tick());
  const reusedResult = await w.submit(blue.sessionId, reusedInvite);
  checks.push(
    w.check(
      "reusing a session id for another invitation is rejected and inherits nothing",
      !reusedResult.ok && reusedResult.reason === "session-id-conflict",
      JSON.stringify(reusedResult),
    ),
  );

  const replayBlue = await w.replayOnB(blue.sessionId);
  const replayRed = await w.replayOnB(red.sessionId);
  checks.push(
    w.check(
      "both group logs replay independently and identically on node-B",
      JSON.stringify(replayBlue) === JSON.stringify(await w.nodeA.getState(blue.sessionId)) &&
        JSON.stringify(replayRed) === JSON.stringify(await w.nodeA.getState(red.sessionId)),
      "replay equality",
    ),
  );

  return {
    scenario: "same-participant-two-groups",
    checks,
    events: [...(await w.events(blue.sessionId)), ...(await w.events(red.sessionId))],
  };
}
