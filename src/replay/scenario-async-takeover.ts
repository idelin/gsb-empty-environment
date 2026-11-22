import { evaluateAnswer, signAnswer, signInvitation } from "../index.ts";
import { World, type ScenarioReport, type ScenarioCheck } from "./world.ts";

/**
 * Scenario 1: asynchronous takeover.
 * Bob disconnects on node A and reconnects on node B while an answer is
 * queued. The three migration facts may arrive in any order; until all three
 * bind in one migration transaction the answer has no deliverable target, and
 * it can never flow to a stranger reusing Bob's display name or address.
 */
export async function scenarioAsyncTakeover(w: World): Promise<ScenarioReport> {
  const sessionId = "takeover-1";
  const invitation = w.invitation(sessionId, "group-blue", w.alice, w.bob);
  const checks: ScenarioCheck[] = [];

  await w.submit(sessionId, signInvitation(w.sig, w.alice, invitation, w.tick()));
  await w.submit(sessionId, w.consent(invitation, w.alice));
  await w.submit(sessionId, w.consent(invitation, w.bob));
  await w.nodeA.registerConnection(sessionId, w.bob.identity, "display-bob", "addr-1", 1);

  const queuedAnswer = signAnswer(
    w.sig,
    w.alice,
    {
      answerId: "a1",
      sessionId,
      invitationDigest: w.digest(invitation),
      group: invitation.group,
      from: w.alice.identity,
      to: w.bob.identity,
      body: "hello",
      createdAt: w.tick(),
    },
    w.tick(),
  );
  await w.submit(sessionId, queuedAnswer);

  const before = await w.nodeA.queryVerdict(sessionId, "a1");
  checks.push(
    w.check(
      "answer deliverable on original node before migration",
      before.status === "deliverable" && before.node === "node-A",
      JSON.stringify(before),
    ),
  );

  const mig = w.migration(invitation, w.bob, w.alice, 1, 2, "node-A", "node-B", "mig-1");
  await w.submit(sessionId, mig.takeover);
  const partial = await w.nodeA.queryVerdict(sessionId, "a1");
  checks.push(
    w.check(
      "partial migration waits for release and approval",
      partial.status === "waiting" &&
        partial.missing.some((m) => m.kind === "migration-release") &&
        partial.missing.some((m) => m.kind === "migration-approval"),
      JSON.stringify(partial),
    ),
  );

  await w.submit(sessionId, mig.release);
  await w.submit(sessionId, mig.approval);

  const after = await w.nodeA.queryVerdict(sessionId, "a1");
  checks.push(
    w.check(
      "completed takeover routes to generation 2 on node-B for the same identity",
      after.status === "deliverable" &&
        after.node === "node-B" &&
        after.generation === 2 &&
        after.to === w.bob.identity,
      JSON.stringify(after),
    ),
  );

  await w.nodeA.registerConnection(sessionId, w.carol.identity, "display-bob", "addr-1", 1);
  const stranger = await w.nodeA.queryVerdict(sessionId, "a1");
  checks.push(
    w.check(
      "reused display name and address never inherit the queued answer",
      stranger.status === "deliverable" && stranger.to === w.bob.identity,
      JSON.stringify(stranger),
    ),
  );

  const replayedB = await w.replayOnB(sessionId);
  const replayedA = await w.nodeA.getState(sessionId);
  checks.push(
    w.check(
      "node-B full replay converges on node-A state",
      JSON.stringify(replayedA) === JSON.stringify(replayedB),
      "state equality",
    ),
  );
  const verdictOnB = evaluateAnswer(replayedB, "a1");
  checks.push(
    w.check(
      "node-B replay computes the same deliverable target",
      verdictOnB.status === "deliverable" &&
        verdictOnB.node === "node-B" &&
        verdictOnB.to === w.bob.identity,
      JSON.stringify(verdictOnB),
    ),
  );

  return { scenario: "async-takeover", checks, events: await w.events(sessionId) };
}
