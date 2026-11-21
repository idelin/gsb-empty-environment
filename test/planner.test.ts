import test from "node:test";
import assert from "node:assert/strict";
import {
  NoFeasiblePlanError,
  compareStableIds,
  enumerateCompatibleHistories,
  planMigration,
  verifyMigration,
  type FrameSpec,
  type MessageSpec,
  type MigrationProblem,
} from "../src/index.ts";

function privateFrame(
  id: string,
  channels: string[],
  candidates: string[],
  audience: string,
  optional = false,
): { frame: FrameSpec; messages: MessageSpec[] } {
  return {
    frame: { id, channels, candidates, optional },
    messages: channels.map((channel, index) => ({
      id: `${id}-m${index + 1}`,
      frame: id,
      channel,
      visibility: "private" as const,
      audience: [audience],
    })),
  };
}

test("two local ambiguities sharing the same bad recipient need one reset", () => {
  const first = privateFrame("f1", ["q1", "q2"], ["1", "2"], "alice");
  const second = privateFrame("f2", ["q3", "q4"], ["1", "2"], "alice");
  const problem: MigrationProblem = {
    instances: [
      { id: "1", principal: "alice", connection: "transport-A" },
      { id: "2", principal: "mallory", connection: "transport-A" },
    ],
    frames: [first.frame, second.frame],
    messages: [...first.messages, ...second.messages],
  };

  const plan = planMigration(problem);
  assert.deepEqual(plan.reauthenticate, ["2"]);
  assert.deepEqual(plan.frameContinuations, { f1: "1", f2: "1" });
  assert.deepEqual(plan.continuations, {
    q1: "1",
    q2: "1",
    q3: "1",
    q4: "1",
  });

  const accepted = verifyMigration(problem, plan);
  assert.equal(accepted.ok, true);
  assert.equal(accepted.safe, true);
  assert.equal(accepted.optimal, true);
  assert.deepEqual(accepted.witnesses.unsafeHistories, []);

  // Choosing instance 1 in the new mapping does not erase the old receipt's
  // compatibility with instance 2. The verifier must reject that false sense
  // of safety.
  const unsafe = verifyMigration(problem, {
    reauthenticate: [],
    continuations: { q1: "1", q2: "1", q3: "1", q4: "1" },
  });
  assert.equal(unsafe.ok, false);
  assert.equal(unsafe.safe, false);
  assert.ok(
    unsafe.witnesses.unsafeHistories.some(
      (witness) => witness.frame === "f1" && witness.instance === "2",
    ),
  );
});

test("a frame cannot pick a different instance per queue", () => {
  const problem: MigrationProblem = {
    instances: [
      { id: "1", principal: "alice" },
      { id: "2", principal: "alice" },
    ],
    frames: [
      {
        id: "f",
        candidates: ["1", "2"],
        channels: ["q1", "q2"],
      },
    ],
    channels: [
      { id: "q1", frame: "f", candidates: ["1"] },
      { id: "q2", frame: "f", candidates: ["2"] },
    ],
    messages: [
      { id: "m1", frame: "f", channel: "q1", visibility: "private", audience: ["alice"] },
      { id: "m2", frame: "f", channel: "q2", visibility: "private", audience: ["alice"] },
    ],
  };

  const perQueue = verifyMigration(problem, {
    reauthenticate: [],
    continuations: { q1: "1", q2: "2" },
  });
  assert.equal(perQueue.ok, false);
  assert.ok(perQueue.errors.some((error) => error.includes("different instances")));
  assert.throws(() => planMigration(problem), NoFeasiblePlanError);
});

test("revocation only closes one generation", () => {
  const problem: MigrationProblem = {
    instances: [
      { id: "1", principal: "alice" },
      { id: "2", principal: "bob" },
    ],
    frames: [{ id: "f", channels: ["q"], candidates: ["1", "2"] }],
    messages: [
      {
        id: "m",
        frame: "f",
        channel: "q",
        visibility: "private",
        audience: ["alice"],
        seq: 4,
      },
    ],
    grants: [
      { id: "g1", from: "alice", to: "2", audience: "alice", generation: 1, seq: 1 },
      { id: "g2", from: "alice", to: "2", audience: "alice", generation: 2, seq: 2 },
    ],
    revocations: [{ id: "r1", grant: "g1", generation: 1, seq: 3 }],
  };

  const plan = planMigration(problem);
  assert.deepEqual(plan.reauthenticate, []);
  assert.equal(verifyMigration(problem, plan).ok, true);
  assert.deepEqual([...enumerateCompatibleHistories(problem)], [
    { f: "1" },
    { f: "2" },
  ]);

  const bothGenerations: MigrationProblem = {
    ...problem,
    revocations: [
      { id: "r1", grant: "g1", generation: 1, seq: 3 },
      { id: "r2", grant: "g2", generation: 2, seq: 3 },
    ],
  };
  assert.deepEqual(planMigration(bothGenerations).reauthenticate, ["2"]);
});

test("delegated capability chains are evaluated at their log positions", () => {
  const problem: MigrationProblem = {
    instances: [
      { id: "1", principal: "alice" },
      { id: "2", principal: "bob" },
      { id: "3", principal: "carol" },
    ],
    frames: [{ id: "f", channels: ["q"], candidates: ["1", "3"] }],
    messages: [
      {
        id: "m",
        frame: "f",
        channel: "q",
        visibility: "private",
        audience: ["alice"],
        seq: 3,
      },
    ],
    grants: [
      { id: "g1", from: "alice", to: "2", audience: "alice", seq: 1 },
      { id: "g2", from: "2", to: "3", audience: "alice", seq: 2 },
    ],
  };

  const delegated = planMigration(problem);
  assert.deepEqual(delegated.reauthenticate, []);
  assert.equal(delegated.continuations.q, "1");
  assert.equal(verifyMigration(problem, delegated).ok, true);

  const chainRevoked: MigrationProblem = {
    ...problem,
    revocations: [{ id: "r", grant: "g1", generation: 1, seq: 2 }],
  };
  const repaired = planMigration(chainRevoked);
  assert.deepEqual(repaired.reauthenticate, ["3"]);
  assert.equal(verifyMigration(chainRevoked, repaired).ok, true);
});

test("re-authenticating one principal does not disturb another on the same connection", () => {
  const problem: MigrationProblem = {
    instances: [
      { id: "1", principal: "alice", connection: "shared-T" },
      { id: "2", principal: "mallory", connection: "shared-T" },
      { id: "3", principal: "bob", connection: "shared-T" },
    ],
    frames: [
      { id: "f1", channels: ["q1"], candidates: ["1", "2"] },
      { id: "f2", channels: ["q2"], candidates: ["3"] },
    ],
    messages: [
      {
        id: "m1",
        frame: "f1",
        channel: "q1",
        visibility: "private",
        audience: ["alice"],
        seq: 2,
      },
      {
        id: "m2",
        frame: "f2",
        channel: "q2",
        visibility: "private",
        audience: ["alice"],
        seq: 2,
      },
    ],
    grants: [{ id: "g3", from: "alice", to: "3", audience: "alice", seq: 1 }],
  };

  const plan = planMigration(problem);
  assert.deepEqual(plan.reauthenticate, ["2"]);
  assert.equal(plan.continuations.q2, "3");
  const verified = verifyMigration(problem, plan);
  assert.equal(verified.ok, true);
});

test("an atomic mutex group is stopped as a whole", () => {
  const first = privateFrame("f1", ["q1"], ["1"], "alice", true);
  const second = privateFrame("f2", ["q2"], ["2"], "alice", true);
  const problem: MigrationProblem = {
    instances: [
      { id: "1", principal: "alice" },
      { id: "2", principal: "mallory", interruptible: false },
    ],
    frames: [first.frame, second.frame],
    messages: [...first.messages, ...second.messages],
    mutexGroups: [{ id: "g", frames: ["f1", "f2"], atomic: true }],
  };

  const plan = planMigration(problem);
  assert.deepEqual(plan.reauthenticate, []);
  assert.deepEqual(plan.stoppedChannels, ["q1", "q2"]);
  assert.deepEqual(plan.stoppedFrames, ["f1", "f2"]);
  assert.deepEqual(plan.stoppedGroups, ["g"]);
  assert.deepEqual(plan.continuations, {});
  assert.equal(verifyMigration(problem, plan).ok, true);

  const withProtectedPublic: MigrationProblem = {
    ...problem,
    messages: [
      ...problem.messages!,
      {
        id: "public",
        frame: "f1",
        channel: "q1",
        visibility: "public",
        confirmed: true,
        protected: true,
      },
    ],
  };
  assert.throws(() => planMigration(withProtectedPublic), NoFeasiblePlanError);
});

test("stable instance ordering is canonical and natural", () => {
  assert.ok(compareStableIds("i2", "i10") < 0);
  const problem: MigrationProblem = {
    instances: [
      { id: "1", principal: "alice" },
      { id: "2", principal: "mallory" },
      { id: "3", principal: "mallory" },
    ],
    frames: [{ id: "f", channels: ["q"], candidates: ["1", "2", "3"] }],
    messages: [
      { id: "m", frame: "f", channel: "q", visibility: "private", audience: ["alice"] },
    ],
  };
  assert.deepEqual(planMigration(problem).reauthenticate, ["2", "3"]);

  const reversed = verifyMigration(problem, { reauthenticate: ["3", "2"] });
  assert.equal(reversed.safe, true);
  assert.equal(reversed.optimal, true);
  assert.equal(reversed.canonical, true);
  assert.deepEqual(reversed.reauthenticate, ["2", "3"]);
});

test("plans with avoidable extra resets are not accepted as optimal", () => {
  const problem: MigrationProblem = {
    instances: [
      { id: "1", principal: "alice" },
      { id: "2", principal: "mallory" },
      { id: "3", principal: "bob" },
    ],
    frames: [{ id: "f", channels: ["q"], candidates: ["1", "2"] }],
    messages: [
      { id: "m", frame: "f", channel: "q", visibility: "private", audience: ["alice"] },
    ],
  };
  const oversized = verifyMigration(problem, { reauthenticate: ["2", "3"] });
  assert.equal(oversized.ok, false);
  assert.equal(oversized.optimal, false);
  assert.deepEqual(oversized.witnesses.smallerReset, ["2"]);
});

test("signed log events can describe the same migration", () => {
  const problem: MigrationProblem = {
    instances: [
      { id: "1", principal: "alice" },
      { id: "2", principal: "mallory" },
    ],
    events: [
      {
        type: "send",
        seq: 1,
        message: "m1",
        frame: "f",
        channel: "q",
        visibility: "private",
        audience: ["alice"],
      },
      { type: "ack", seq: 2, message: "m1", candidates: ["1", "2"] },
    ],
  };
  const plan = planMigration(problem);
  assert.deepEqual(plan.reauthenticate, ["2"]);
  assert.equal(verifyMigration(problem, plan).ok, true);
});
