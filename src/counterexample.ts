/**
 * Counterexample: why the switch cannot skip the joint (intermediate)
 * confirmation.
 *
 * Old config {A,B,C} and new config {D,E,F} are DISJOINT, so an old
 * majority {A,B} and a new majority {D,E} do not intersect. If C_new
 * could commit under the old majority alone, both groups could commit
 * different entries at the same log index -> split brain.
 *
 * Run: node src/counterexample.ts
 */

import { Leader, SafetyTracker } from "./simulator.ts";

const OLD = ["A", "B", "C"];
const NEW = ["D", "E", "F"];

function line(msg: string): void {
  console.log(msg);
}

function unsafeScenario(): void {
  line("=== Buggy switch (skips the joint stage) ===");
  const tracker = new SafetyTracker();
  const oldLeader = new Leader("A", OLD, tracker, 1);

  const committedX = oldLeader.proposeData("x", ["B"]);
  line(`old group commits "x"@1 with acks {A,B} (old majority): ${committedX}`);

  const switched = oldLeader.unsafeDirectSwitch(NEW, ["B"]);
  line(`C_new committed with OLD majority {A,B} only: ${switched}`);
  line(`old leader now considers the config to be ${JSON.stringify(NEW)}`);

  // The new group activates without ever having seen "x"@1.
  const newLeader = new Leader("D", NEW, tracker, 2);
  line("new group elects D with majority {D,E}; D's log is empty");
  try {
    newLeader.proposeData("y", ["E"]);
    line("BUG NOT REPRODUCED: no conflict detected");
  } catch (err) {
    line(`!! ${(err as Error).message}`);
    line("=> old majority and new majority committed different entries");
    line("   at the same index: split brain.\n");
  }
}

function jointScenario(): void {
  line("=== Joint-consensus switch (safe) ===");
  const tracker = new SafetyTracker();
  const leader = new Leader("A", OLD, tracker, 1);

  line(`stage: ${JSON.stringify(leader.stage)}`);
  const committedX = leader.proposeData("x", ["B"]);
  line(`old group commits "x"@1 with acks {A,B}: ${committedX}`);

  const jointCommitted = leader.proposeJointConfig(NEW, ["B"]);
  line(`C_old,new committed under old rules with acks {A,B}: ${jointCommitted}`);
  line(`stage: ${JSON.stringify(leader.stage)}`);

  // The exact counterexample quorum: old majority YES, new majority NO.
  const finalizeOldOnly = leader.finalizeConfig(["B", "C"]);
  line(
    `finalize C_new with acks {A,B,C} (full old group, zero new members): ` +
      `${finalizeOldOnly}  <-- blocked`,
  );
  const finalizeOldPlusOne = leader.finalizeConfig(["B", "D"]);
  line(
    `finalize C_new with acks {A,B,D} (old majority, new minority): ` +
      `${finalizeOldPlusOne}  <-- blocked`,
  );
  const finalizeBoth = leader.finalizeConfig(["B", "D", "E"]);
  line(
    `finalize C_new with acks {A,B,D,E} (both majorities): ` +
      `${finalizeBoth}  <-- committed`,
  );
  line(`stage: ${JSON.stringify(leader.stage)}`);

  const committedZ = leader.proposeData("z", ["D", "E"]);
  line(`new group commits "z" under new config with acks {D,E}: ${committedZ}`);
  line("=> no index was ever committed by two different quorums: safe.\n");
}

unsafeScenario();
jointScenario();
