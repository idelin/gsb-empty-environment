import assert from "node:assert/strict";
import test from "node:test";

import {
  evaluateRecovery,
  findMinimumDestructionSet,
  planDestruction,
  validateSnapshot,
  verifyDestructionCertificate,
} from "../src/index.ts";
import * as packageEntry from "../index.ts";
import type {
  KeyNode,
  KeySnapshot,
  RecoveryWitness,
} from "../src/index.ts";
import {
  referencePlan,
  referenceRecovered,
} from "./reference.ts";

function objectIds(...ids: string[]): { id: string }[] {
  return ids.map((id) => ({ id }));
}

function material(
  id: string,
  objectId?: string,
  revoked?: boolean,
): KeyNode {
  return {
    id,
    kind: "material",
    ...(objectId === undefined ? {} : { objectId }),
    ...(revoked === undefined ? {} : { revoked }),
  };
}

function stored(id: string, objectId: string): KeyNode {
  return { id, kind: "stored", objectId };
}

function derived(id: string, inputs: string[]): KeyNode {
  return { id, kind: "derived", inputs };
}

function sealed(
  id: string,
  backup: string,
  wrappingKeys: string[],
): KeyNode {
  return { id, kind: "sealed", backup, wrappingKeys };
}

function threshold(
  id: string,
  required: number,
  shards: {
    nodeId: string;
    round: string;
    index: string | number;
    objectId?: string;
  }[],
): KeyNode {
  return {
    id,
    kind: "threshold",
    threshold: {
      required,
      shards,
    },
  };
}

function onlyWitness(
  witness: RecoveryWitness | null,
): RecoveryWitness {
  assert.ok(witness);
  return witness;
}

test("the package entry point re-exports the checker API", () => {
  assert.equal(typeof packageEntry.planDestruction, "function");
  assert.equal(typeof packageEntry.verifyDestructionCertificate, "function");
});

test("cross-round shards cannot be mixed and aliases count as one object", () => {
  const snapshot: KeySnapshot = {
    targetNodeId: "target",
    objects: objectIds("o1", "o2", "o3", "o4"),
    nodes: [
      material("s1", "o1"),
      material("s1-alias", "o1"),
      material("s1-other-index", "o1"),
      material("s2", "o2"),
      material("s3", "o3"),
      material("s4", "o4"),
      threshold("target", 2, [
        { nodeId: "s1", round: "r1", index: 1 },
        { nodeId: "s1-alias", round: "r1", index: 1 },
        { nodeId: "s1-other-index", round: "r1", index: 2 },
        { nodeId: "s2", round: "r1", index: 2 },
        { nodeId: "s3", round: "r2", index: 2 },
        { nodeId: "s4", round: "r2", index: 3 },
      ]),
    ],
  };

  assert.deepEqual(validateSnapshot(snapshot), []);
  const crossed = evaluateRecovery(snapshot, ["o2", "o4"]);
  assert.equal(crossed.recovered.has("target"), false);

  const aliased = evaluateRecovery(snapshot, ["o2", "o3", "o4"]);
  assert.equal(aliased.recovered.has("target"), false);

  const plan = planDestruction(snapshot);
  assert.equal(plan.status, "planned");
  if (plan.status === "planned") {
    assert.deepEqual(plan.deletedObjectIds, ["o1", "o3"]);
  }
});

test("a rootless cycle stays unrecoverable and verifies with an empty certificate", () => {
  const snapshot: KeySnapshot = {
    targetNodeId: "a",
    objects: objectIds("unused"),
    nodes: [derived("a", ["b"]), derived("b", ["a"])],
  };

  const plan = planDestruction(snapshot);
  assert.equal(plan.status, "already-unrecoverable");
  assert.deepEqual(findMinimumDestructionSet(snapshot), []);

  const empty = verifyDestructionCertificate(snapshot, {
    deletedObjectIds: [],
  });
  assert.equal(empty.valid, true);
  assert.equal(empty.status, "already-unrecoverable");

  const nonEmpty = verifyDestructionCertificate(snapshot, ["unused"]);
  assert.equal(nonEmpty.valid, false);
  assert.equal(nonEmpty.code, "target-already-unrecoverable");
});

test("a shared wrapper is preserved unless both private backups must be deleted", () => {
  const snapshot: KeySnapshot = {
    targetNodeId: "target",
    objects: objectIds("backup-one", "backup-two", "shared-wrapper"),
    nodes: [
      stored("b1", "backup-one"),
      stored("b2", "backup-two"),
      material("w", "shared-wrapper"),
      sealed("e1", "b1", ["w"]),
      sealed("e2", "b2", ["w"]),
      derived("other-tenant", ["w"]),
      threshold("target", 1, [
        { nodeId: "e1", round: "r1", index: 1 },
        { nodeId: "e2", round: "r1", index: 2 },
      ]),
    ],
    mustRemainRecoverable: ["other-tenant"],
  };

  const targetOnlyDeletion = evaluateRecovery(snapshot, ["shared-wrapper"]);
  assert.equal(targetOnlyDeletion.recovered.has("target"), false);
  assert.equal(targetOnlyDeletion.recovered.has("other-tenant"), false);

  const plan = planDestruction(snapshot);
  assert.equal(plan.status, "planned");
  if (plan.status === "planned") {
    assert.deepEqual(plan.deletedObjectIds, ["backup-one", "backup-two"]);
  }

  const collateral = verifyDestructionCertificate(snapshot, [
    "shared-wrapper",
  ]);
  assert.equal(collateral.valid, false);
  assert.equal(collateral.code, "preservation-constraint");
  assert.deepEqual(
    collateral.mandatoryFailures.map((failure) => failure.keyId),
    ["other-tenant"],
  );
});

test("multi-layer sealing produces a complete recovery witness", () => {
  const snapshot: KeySnapshot = {
    targetNodeId: "target",
    objects: objectIds("b1-object", "b2-object", "k2-object"),
    nodes: [
      stored("b1", "b1-object"),
      stored("b2", "b2-object"),
      stored("k2", "k2-object"),
      sealed("k1", "b1", ["k2"]),
      sealed("target", "b2", ["k1"]),
    ],
  };

  const before = evaluateRecovery(snapshot);
  const witness = onlyWitness(before.witnessFor("target"));
  assert.deepEqual(
    witness.steps.map((step) => step.nodeId),
    ["b2", "b1", "k2", "k1", "target"],
  );

  const plan = planDestruction(snapshot);
  assert.equal(plan.status, "planned");
  if (plan.status === "planned") {
    assert.deepEqual(plan.deletedObjectIds, ["b1-object"]);
    const certificate = verifyDestructionCertificate(
      snapshot,
      plan.deletedObjectIds,
    );
    assert.equal(certificate.valid, true);
    assert.equal(certificate.restoreWitnesses.length, 1);
    assert.equal(certificate.restoreWitnesses[0].targetRecoverable, true);
    assert.ok(certificate.restoreWitnesses[0].targetWitness);
  }
});

test("an exported copy survives revocation and prevents destruction", () => {
  const snapshot: KeySnapshot = {
    targetNodeId: "target",
    objects: objectIds("target-object"),
    nodes: [material("target", "target-object", true)],
    exportedCopies: [
      { id: "exported-copy-1", keyId: "target", revoked: true },
    ],
    revokedCredentials: ["revoked-reader"],
  };

  const plan = planDestruction(snapshot);
  assert.equal(plan.status, "impossible");
  if (plan.status === "impossible") {
    assert.equal(plan.code, "exported-copy");
    assert.deepEqual(plan.exportedCopyIds, ["exported-copy-1"]);
  }

  const rejected = verifyDestructionCertificate(snapshot, ["target-object"]);
  assert.equal(rejected.valid, false);
  assert.equal(rejected.code, "exported-copy");
  assert.deepEqual(rejected.exportedCopyIds, ["exported-copy-1"]);
});

test("revocation alone does not make a stored key unrecoverable", () => {
  const snapshot: KeySnapshot = {
    targetNodeId: "target",
    objects: objectIds("target-object"),
    nodes: [material("target", "target-object", true)],
    revokedCredentials: ["reader"],
  };

  const plan = planDestruction(snapshot);
  assert.equal(plan.status, "planned");
  if (plan.status === "planned") {
    assert.deepEqual(plan.deletedObjectIds, ["target-object"]);
  }
});

test("non-deletable known input is distinguished from an exported copy", () => {
  const snapshot: KeySnapshot = {
    targetNodeId: "target",
    objects: objectIds("unused"),
    nodes: [material("target")],
  };

  const plan = planDestruction(snapshot);
  assert.equal(plan.status, "impossible");
  if (plan.status === "impossible") {
    assert.equal(plan.code, "intrinsic-recovery");
    assert.deepEqual(plan.exportedCopyIds, []);
    assert.ok(plan.allObjectsDeletedTargetWitness);
  }
});

test("certificate verifier rejects non-minimal and non-lexicographic sets", () => {
  const snapshot: KeySnapshot = {
    targetNodeId: "target",
    objects: objectIds("aa", "bb"),
    nodes: [
      material("a", "aa"),
      material("b", "bb"),
      derived("target", ["a", "b"]),
    ],
  };

  const nonMinimum = verifyDestructionCertificate(snapshot, ["aa", "bb"]);
  assert.equal(nonMinimum.valid, false);
  assert.equal(nonMinimum.code, "not-minimal");
  assert.equal(nonMinimum.restoreWitnesses.length, 2);
  assert.ok(nonMinimum.restoreWitnesses.every((witness) => !witness.targetRecoverable));

  const wrongTie = verifyDestructionCertificate(snapshot, ["bb"]);
  assert.equal(wrongTie.valid, false);
  assert.equal(wrongTie.code, "not-lexicographically-minimal");

  const valid = verifyDestructionCertificate(snapshot, ["aa"]);
  assert.equal(valid.valid, true);
  assert.equal(valid.code, "certified");
});

test("unrecoverable mandatory keys are reported as a preservation constraint", () => {
  const snapshot: KeySnapshot = {
    targetNodeId: "target",
    objects: objectIds("shared"),
    nodes: [
      material("input", "shared"),
      derived("target", ["input"]),
      derived("required", ["input"]),
    ],
    mustRemainRecoverable: ["required"],
  };

  const plan = planDestruction(snapshot);
  assert.equal(plan.status, "impossible");
  if (plan.status === "impossible") {
    assert.equal(plan.code, "preservation-constraint");
    assert.deepEqual(
      plan.blockingMandatoryKeys.map((failure) => failure.keyId),
      ["required"],
    );
  }
});

function nextRandom(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state ^= state << 13;
    state ^= state >>> 17;
    state ^= state << 5;
    return (state >>> 0) / 0x1_0000_0000;
  };
}

function pick<T>(random: () => number, values: readonly T[]): T {
  return values[Math.floor(random() * values.length)];
}

function randomInt(random: () => number, minimum: number, maximum: number): number {
  return minimum + Math.floor(random() * (maximum - minimum + 1));
}

function randomSnapshot(seed: number): KeySnapshot {
  const random = nextRandom(seed);
  const objectCount = randomInt(random, 3, 6);
  const nodeCount = randomInt(random, 6, 9);
  const objectIdList = Array.from(
    { length: objectCount },
    (_, index) => `o${index}`,
  );
  const nodeIdList = Array.from(
    { length: nodeCount },
    (_, index) => `n${index}`,
  );

  const nodes: KeyNode[] = nodeIdList.map((id): KeyNode => {
    const choice = random();
    if (choice < 0.3) {
      return material(
        id,
        random() < 0.7 ? pick(random, objectIdList) : undefined,
        random() < 0.1,
      );
    }
    if (choice < 0.42) {
      return stored(id, pick(random, objectIdList));
    }
    if (choice < 0.64) {
      const inputs = Array.from(
        { length: randomInt(random, 1, 3) },
        () => pick(random, nodeIdList),
      );
      return {
        id,
        kind: "derived",
        inputs,
        ...(random() < 0.5 ? { objectId: pick(random, objectIdList) } : {}),
      };
    }
    if (choice < 0.76) {
      return {
        id,
        kind: "sealed",
        backup: pick(random, nodeIdList),
        wrappingKeys: Array.from(
          { length: randomInt(random, 1, 2) },
          () => pick(random, nodeIdList),
        ),
        ...(random() < 0.5 ? { objectId: pick(random, objectIdList) } : {}),
      };
    }
    if (choice < 0.9) {
      const shardCount = randomInt(random, 2, 4);
      const required = randomInt(random, 1, Math.min(3, shardCount));
      const rounds = ["r0", "r1"];
      return {
        id,
        kind: "threshold",
        threshold: {
          required,
          shards: Array.from({ length: shardCount }, () => ({
            nodeId: pick(random, nodeIdList),
            round: pick(random, rounds),
            index: randomInt(random, 1, 3),
            ...(random() < 0.4
              ? { objectId: pick(random, objectIdList) }
              : {}),
          })),
        },
        ...(random() < 0.5 ? { objectId: pick(random, objectIdList) } : {}),
      };
    }
    if (choice < 0.97) {
      return {
        id,
        kind: "alias",
        source: pick(random, nodeIdList),
        ...(random() < 0.5 ? { objectId: pick(random, objectIdList) } : {}),
      };
    }
    return { id, kind: "export", source: pick(random, nodeIdList) };
  });

  const mandatory = Array.from(
    { length: randomInt(random, 0, 2) },
    () => pick(random, nodeIdList),
  );
  const copies = random() < 0.15
    ? [{ id: "copy", keyId: pick(random, nodeIdList), revoked: random() < 0.5 }]
    : [];

  return {
    targetNodeId: pick(random, nodeIdList),
    objects: objectIdList.map((id) => ({ id })),
    nodes,
    mustRemainRecoverable: mandatory,
    exportedCopies: copies,
  };
}

test("planner matches independent exhaustive reference on random small graphs", () => {
  for (let seed = 1; seed <= 250; seed += 1) {
    const snapshot = randomSnapshot(seed);
    assert.deepEqual(validateSnapshot(snapshot), [], `seed ${seed}`);
    const expected = referencePlan(snapshot);
    const actual = planDestruction(snapshot);

    if (expected.kind === "planned") {
      assert.equal(actual.status, "planned", `seed ${seed}`);
      if (actual.status === "planned") {
        assert.deepEqual(
          actual.deletedObjectIds,
          expected.deletedObjectIds,
          `seed ${seed}`,
        );
        const verification = verifyDestructionCertificate(
          snapshot,
          actual.deletedObjectIds,
        );
        assert.equal(verification.valid, true, `seed ${seed}`);
      }
    } else if (expected.kind === "already-unrecoverable") {
      assert.equal(actual.status, "already-unrecoverable", `seed ${seed}`);
    } else {
      assert.equal(actual.status, "impossible", `seed ${seed}`);
    }
  }
});

test("recovery and reference evaluator agree on all deletion subsets", () => {
  for (let seed = 1; seed <= 80; seed += 1) {
    const snapshot = randomSnapshot(seed + 1000);
    const objectIdList = snapshot.objects.map((object) => object.id);
    const subsetCount = 1 << objectIdList.length;
    for (let mask = 0; mask < subsetCount; mask += 1) {
      const deleted = objectIdList.filter(
        (_, index) => (mask & (1 << index)) !== 0,
      );
      const actual = evaluateRecovery(snapshot, deleted).recoveredNodeIds;
      const expected = [...referenceRecovered(snapshot, deleted)].sort();
      assert.deepEqual(actual, expected, `seed ${seed} mask ${mask}`);
    }
  }
});
