import { signAnswer, signInvitation } from "../index.ts";
import { World, type ScenarioReport, type ScenarioCheck } from "./world.ts";

/**
 * Scenario 2: crossed invitations.
 * Alice and Bob invite each other at the same time. Each invitation gets its
 * own session id and digest; answers stay bound to their original invitation
 * and cannot cross between logs, even when participants and group are equal.
 */
export async function scenarioCrossedInvitations(w: World): Promise<ScenarioReport> {
  const checks: ScenarioCheck[] = [];
  const invA = w.invitation("cross-a", "group-red", w.alice, w.bob);
  const invB = w.invitation("cross-b", "group-red", w.bob, w.alice);

  await w.submit("cross-a", signInvitation(w.sig, w.alice, invA, w.tick()));
  await w.submit("cross-b", signInvitation(w.sig, w.bob, invB, w.tick()));
  await w.submit("cross-a", w.consent(invA, w.alice));
  await w.submit("cross-a", w.consent(invA, w.bob));
  await w.submit("cross-b", w.consent(invB, w.bob));
  await w.submit("cross-b", w.consent(invB, w.alice));

  const answerForA = signAnswer(
    w.sig,
    w.alice,
    {
      answerId: "xa-1",
      sessionId: "cross-a",
      invitationDigest: w.digest(invA),
      group: "group-red",
      from: w.alice.identity,
      to: w.bob.identity,
      body: "for session A",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  const answerForB = signAnswer(
    w.sig,
    w.bob,
    {
      answerId: "xb-1",
      sessionId: "cross-b",
      invitationDigest: w.digest(invB),
      group: "group-red",
      from: w.bob.identity,
      to: w.alice.identity,
      body: "for session B",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  await w.submit("cross-a", answerForA);
  await w.submit("cross-b", answerForB);

  const wrongDirection = signAnswer(
    w.sig,
    // Signed by Alice but claiming to be from Bob: signer and answer sender
    // disagree, so the relay must refuse it even though both are parties.
    w.alice,
    {
      answerId: "xa-replay",
      sessionId: "cross-b",
      invitationDigest: w.digest(invB),
      group: "group-red",
      from: w.bob.identity,
      to: w.alice.identity,
      body: "crossed wires",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  const rejectedDirection = await w.submit("cross-b", wrongDirection);
  checks.push(
    w.check(
      "an answer whose signer does not match its sender is rejected",
      !rejectedDirection.ok && rejectedDirection.reason === "wrong-signer",
      JSON.stringify(rejectedDirection),
    ),
  );

  const digestSwap = signAnswer(
    w.sig,
    w.bob,
    {
      answerId: "xb-swap",
      sessionId: "cross-b",
      invitationDigest: w.digest(invA),
      group: "group-red",
      from: w.bob.identity,
      to: w.alice.identity,
      body: "foreign digest",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  const swapped = await w.submit("cross-b", digestSwap);
  checks.push(
    w.check(
      "an answer bound to another invitation digest is rejected",
      !swapped.ok && swapped.reason === "invitation-digest-mismatch",
      JSON.stringify(swapped),
    ),
  );

  const stateA = await w.nodeA.getState("cross-a");
  const stateB = await w.nodeA.getState("cross-b");
  checks.push(
    w.check(
      "crossed invitations keep independent session logs and answer routing",
      stateA.answers["xa-1"] !== undefined && stateB.answers["xb-1"] !== undefined,
      "separate logs",
    ),
  );

  const replayA = await w.replayOnB("cross-a");
  const replayB = await w.replayOnB("cross-b");
  checks.push(
    w.check(
      "both logs replay identically on node-B",
      JSON.stringify(replayA) === JSON.stringify(stateA) &&
        JSON.stringify(replayB) === JSON.stringify(stateB),
      "replay equality",
    ),
  );

  return {
    scenario: "crossed-invitations",
    checks,
    events: [...(await w.events("cross-a")), ...(await w.events("cross-b"))],
  };
}
