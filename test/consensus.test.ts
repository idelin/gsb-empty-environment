import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  ConfigStateMachine,
  isCommitted,
  isMajority,
  majorityOf,
  type Stage,
} from "../src/consensus.ts";
import { Leader, SafetyTracker } from "../src/simulator.ts";

const OLD = ["A", "B", "C"];
const NEW = ["D", "E", "F"];

const joint: Stage = { kind: "joint", oldConfig: OLD, newConfig: NEW };

describe("quorum math", () => {
  it("computes majorities", () => {
    assert.equal(majorityOf(1), 1);
    assert.equal(majorityOf(2), 2);
    assert.equal(majorityOf(3), 2);
    assert.equal(majorityOf(4), 3);
    assert.equal(majorityOf(5), 3);
  });

  it("checks majority membership", () => {
    assert.ok(isMajority(OLD, new Set(["A", "B"])));
    assert.ok(!isMajority(OLD, new Set(["A"])));
    // Voters outside the config do not count.
    assert.ok(!isMajority(OLD, new Set(["A", "D"])));
  });
});

describe("commit decision per stage", () => {
  it("stable stage commits with a majority of the current config", () => {
    const stable: Stage = { kind: "stable", config: OLD };
    assert.ok(isCommitted(stable, new Set(["A", "B"])));
    assert.ok(!isCommitted(stable, new Set(["A"])));
  });

  it("joint stage requires BOTH majorities", () => {
    // The counterexample quorum: old group fully acks, new group does not.
    assert.ok(!isCommitted(joint, new Set(["A", "B", "C"])));
    // Old majority + new minority is still not enough.
    assert.ok(!isCommitted(joint, new Set(["A", "B", "D"])));
    // Mirror image: new majority only.
    assert.ok(!isCommitted(joint, new Set(["D", "E"])));
    // Both majorities: committed.
    assert.ok(isCommitted(joint, new Set(["A", "B", "D", "E"])));
  });
});

describe("config state machine", () => {
  it("walks stable -> joint -> stable(new) only via committed config entries", () => {
    const fsm = new ConfigStateMachine(OLD);
    assert.deepEqual(fsm.stage, { kind: "stable", config: OLD });

    const jointPayload = fsm.proposeJoint(NEW);
    fsm.applyCommitted({ index: 1, term: 1, payload: jointPayload });
    assert.deepEqual(fsm.stage, {
      kind: "joint",
      oldConfig: OLD,
      newConfig: NEW,
    });

    const finalPayload = fsm.proposeFinalize();
    fsm.applyCommitted({ index: 2, term: 1, payload: finalPayload });
    assert.deepEqual(fsm.stage, { kind: "stable", config: NEW });
  });

  it("has no direct stable(old) -> stable(new) transition", () => {
    const fsm = new ConfigStateMachine(OLD);
    // Cannot finalize before entering the joint stage.
    assert.throws(() => fsm.proposeFinalize(), /not in the joint stage/);
    assert.throws(
      () =>
        fsm.applyCommitted({
          index: 1,
          term: 1,
          payload: { type: "config-new" },
        }),
      /outside joint stage/,
    );
    // Cannot start a second reconfiguration while one is in flight.
    fsm.applyCommitted({ index: 1, term: 1, payload: fsm.proposeJoint(NEW) });
    assert.throws(() => fsm.proposeJoint(["G"]), /in flight/);
  });
});

describe("config entries commit per stage rules", () => {
  it("C_old,new commits under the OLD config rules", () => {
    const leader = new Leader("A", OLD, new SafetyTracker(), 1);
    // Old majority {A,B}, zero new-group votes: enough for C_old,new.
    assert.ok(leader.proposeJointConfig(NEW, ["B"]));
    assert.equal(leader.stage.kind, "joint");
  });

  it("C_new commits only under the JOINT rules (both majorities)", () => {
    const leader = new Leader("A", OLD, new SafetyTracker(), 1);
    leader.proposeJointConfig(NEW, ["B"]);

    // Old majority alone cannot finalize the switch.
    assert.ok(!leader.finalizeConfig(["B", "C"]));
    assert.equal(leader.stage.kind, "joint");
    // Old majority + new minority cannot either.
    assert.ok(!leader.finalizeConfig(["B", "D"]));
    assert.equal(leader.stage.kind, "joint");
    // Both majorities finalize it.
    assert.ok(leader.finalizeConfig(["B", "D", "E"]));
    assert.deepEqual(leader.stage, { kind: "stable", config: NEW });
  });
});

describe("end-to-end membership switch", () => {
  it("commits data at every stage under the stage's quorum rule", () => {
    const tracker = new SafetyTracker();
    const leader = new Leader("A", OLD, tracker, 1);

    assert.ok(leader.proposeData("before", ["B"])); // old majority
    assert.ok(leader.proposeJointConfig(NEW, ["B"])); // old majority
    // In the joint stage a bare old majority no longer commits data.
    assert.ok(!leader.proposeData("during", ["B", "C"]));
    assert.ok(leader.proposeData("during", ["B", "D", "E"])); // both
    assert.ok(leader.finalizeConfig(["B", "D", "E"])); // both
    // After the switch the new config rules alone apply.
    assert.ok(leader.proposeData("after", ["D", "E"])); // new majority
    assert.ok(!leader.proposeData("after-2", ["B", "C"])); // old is irrelevant

    assert.equal(tracker.committedAt(1), "before");
    assert.equal(tracker.committedAt(4), "during");
    assert.equal(tracker.committedAt(6), "after");
  });
});

describe("counterexample: skipping the joint stage is unsafe", () => {
  const runUnsafe = (): SafetyTracker => {
    const tracker = new SafetyTracker();
    const oldLeader = new Leader("A", OLD, tracker, 1);
    oldLeader.proposeData("x", ["B"]); // committed by old majority {A,B}
    // Buggy: C_new committed by the old majority alone.
    assert.ok(oldLeader.unsafeDirectSwitch(NEW, ["B"]));
    // The new group activates without "x" and commits its own entry @1.
    const newLeader = new Leader("D", NEW, tracker, 2);
    newLeader.proposeData("y", ["E"]); // committed by new majority {D,E}
    return tracker;
  };

  it("direct switch lets two disjoint majorities commit conflicting entries", () => {
    assert.throws(() => runUnsafe(), /SAFETY VIOLATION/);
  });

  it("joint consensus blocks the same scenario at the intermediate stage", () => {
    const tracker = new SafetyTracker();
    const leader = new Leader("A", OLD, tracker, 1);
    leader.proposeData("x", ["B"]);
    leader.proposeJointConfig(NEW, ["B"]);
    // The exact counterexample quorum from the unsafe run — old majority
    // {A,B} plus zero new-group votes — cannot commit C_new any more.
    assert.ok(!leader.finalizeConfig(["B"]));
    assert.ok(!leader.finalizeConfig(["B", "C"]));
    // The new group is never activated, so no second quorum exists.
    assert.equal(leader.stage.kind, "joint");
    assert.equal(tracker.committedAt(1), "x");
    assert.equal(tracker.committedAt(2), undefined); // no conflicting commit
  });
});
