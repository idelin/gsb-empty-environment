import assert from "node:assert/strict";
import test from "node:test";
import {
  ACL_REASONS,
  SparkplugAcl,
  extractBdSeq,
  isRebirthCommand,
  parseSparkplugTopic,
  topicMatchesFilter,
} from "../index.ts";

const app = { kind: "application", clientId: "primary-host" };
const edge = { kind: "edge-node", clientId: "node-1", groupId: "plant-a" };
const otherEdge = { kind: "edge-node", clientId: "node-2", groupId: "plant-a" };

function birthPayload(sequence: number | bigint) {
  return {
    metrics: [{ name: "bdSeq", type: "Int64", value: Number(sequence) }],
  };
}

function deathPayload(sequence: number | bigint) {
  return {
    metrics: [{ name: "bdSeq", type: "Int64", value: Number(sequence) }],
  };
}

function rebirthPayload(value = true) {
  return {
    metrics: [{ name: "Node Control/Rebirth", type: "Boolean", value }],
  };
}

function varint(value: bigint): number[] {
  const bytes: number[] = [];
  let current = value;
  do {
    let byte = Number(current & 0x7fn);
    current >>= 7n;
    if (current > 0n) byte |= 0x80;
    bytes.push(byte);
  } while (current > 0n);
  return bytes;
}

function field(fieldNumber: number, wireType: number, data: number[]): number[] {
  const tag = (BigInt(fieldNumber) << 3n) | BigInt(wireType);
  return [...varint(tag), ...data];
}

function lengthDelimited(fieldNumber: number, data: number[]): number[] {
  return field(fieldNumber, 2, [...varint(BigInt(data.length)), ...data]);
}

function utf8(value: string): number[] {
  return [...new TextEncoder().encode(value)];
}

function protobufMetric(name: string, valueField: number, value: bigint): Uint8Array {
  return Uint8Array.from([
    ...lengthDelimited(1, utf8(name)),
    ...field(valueField, 0, varint(value)),
  ]);
}

function protobufBirth(sequence: bigint): Uint8Array {
  return Uint8Array.from(
    lengthDelimited(2, [...protobufMetric("bdSeq", 11, sequence)]),
  );
}

function protobufRebirth(value: boolean): Uint8Array {
  return Uint8Array.from(
    lengthDelimited(2, [
      ...lengthDelimited(1, utf8("Node Control/Rebirth")),
      ...field(14, 0, value ? [1] : [0]),
    ]),
  );
}

test("topic parsing enforces the Sparkplug shape", () => {
  const topic = parseSparkplugTopic("spBv1.0/plant-a/NDATA/node-1");
  assert.equal(topic?.groupId, "plant-a");
  assert.equal(topic?.messageType, "NDATA");
  assert.equal(topic?.nodeId, "node-1");
  assert.equal(topic?.deviceId, undefined);

  assert.deepEqual(parseSparkplugTopic("spBv1.0/plant-a/NDATA/node-1/device-1")?.deviceId, "device-1");
  assert.equal(parseSparkplugTopic("spBv1.0/plant-a/NBIRTH/node-1/extra"), null);
  assert.equal(parseSparkplugTopic("spBv1.0/plant-a/NCMD/+"), null);
  assert.equal(parseSparkplugTopic("wrong/plant-a/NBIRTH/node-1"), null);
});

test("only application credentials may publish NCMD and DCMD", () => {
  const acl = new SparkplugAcl();

  assert.equal(acl.canPublish(app, "spBv1.0/plant-a/NCMD/node-1"), true);
  assert.equal(
    acl.canPublish(app, "spBv1.0/plant-a/DCMD/node-1/device-1"),
    true,
  );

  for (const topic of [
    "spBv1.0/plant-a/NCMD/node-1",
    "spBv1.0/plant-a/DCMD/node-1/device-1",
  ]) {
    const decision = acl.evaluatePublish(edge, topic, {});
    assert.equal(decision.allowed, false);
    assert.equal(decision.code, ACL_REASONS.COMMAND_PUBLISH_FORBIDDEN);
  }
});

test("edge nodes may only publish their own NBIRTH, NDATA, and NDEATH", () => {
  const acl = new SparkplugAcl();

  const birth = acl.evaluatePublish(
    edge,
    "spBv1.0/plant-a/NBIRTH/node-1",
    birthPayload(7),
  );
  assert.equal(birth.allowed, true);
  assert.equal(acl.getLatestBirthSequence("plant-a", "node-1"), 7n);

  assert.equal(
    acl.canPublish(edge, "spBv1.0/plant-a/NDATA/node-1", { metrics: [] }),
    true,
  );
  assert.equal(
    acl.canPublish(edge, "spBv1.0/plant-a/NDATA/node-1/device-9", {}),
    true,
  );

  const wrongGroup = acl.evaluatePublish(
    edge,
    "spBv1.0/plant-b/NBIRTH/node-1",
    birthPayload(8),
  );
  assert.equal(wrongGroup.allowed, false);
  assert.equal(wrongGroup.code, ACL_REASONS.EDGE_GROUP_MISMATCH);

  const impersonation = acl.evaluatePublish(
    edge,
    "spBv1.0/plant-a/NBIRTH/node-2",
    birthPayload(8),
  );
  assert.equal(impersonation.allowed, false);
  assert.equal(impersonation.code, ACL_REASONS.EDGE_NODE_MISMATCH);

  const forbiddenType = acl.evaluatePublish(
    edge,
    "spBv1.0/plant-a/DBIRTH/node-1/device-1",
    {},
  );
  assert.equal(forbiddenType.allowed, false);
  assert.equal(forbiddenType.code, ACL_REASONS.EDGE_TOPIC_FORBIDDEN);
});

test("NDEATH requires an existing NBIRTH and the exact latest bdSeq", () => {
  const acl = new SparkplugAcl();
  const topic = "spBv1.0/plant-a/NDEATH/node-1";

  const beforeBirth = acl.evaluatePublish(edge, topic, deathPayload(1));
  assert.equal(beforeBirth.allowed, false);
  assert.equal(beforeBirth.code, ACL_REASONS.DEATH_WITHOUT_BIRTH);

  assert.equal(
    acl.canPublish(edge, "spBv1.0/plant-a/NBIRTH/node-1", birthPayload(1)),
    true,
  );
  assert.equal(acl.canPublish(edge, topic, deathPayload(1)), true);

  const forged = acl.evaluatePublish(edge, topic, deathPayload(2));
  assert.equal(forged.allowed, false);
  assert.equal(forged.code, ACL_REASONS.DEATH_SEQUENCE_MISMATCH);

  assert.equal(
    acl.canPublish(edge, "spBv1.0/plant-a/NBIRTH/node-1", birthPayload(2)),
    true,
  );
  assert.equal(acl.canPublish(edge, topic, deathPayload(2)), true);
});

test("NDEATH cannot be forged with a missing or invalid bdSeq", () => {
  const acl = new SparkplugAcl();
  assert.equal(
    acl.canPublish(edge, "spBv1.0/plant-a/NBIRTH/node-1", birthPayload(3)),
    true,
  );

  const cases: Array<[unknown, string]> = [
    [{}, ACL_REASONS.BIRTH_SEQUENCE_MISSING],
    [{ metrics: [] }, ACL_REASONS.BIRTH_SEQUENCE_MISSING],
    [
      { metrics: [{ name: "bdSeq", type: "Int64" }] },
      ACL_REASONS.BIRTH_SEQUENCE_INVALID,
    ],
    [
      { metrics: [{ name: "bdSeq", type: "Int64", value: -1 }] },
      ACL_REASONS.BIRTH_SEQUENCE_INVALID,
    ],
    ["not a payload", ACL_REASONS.BIRTH_SEQUENCE_INVALID],
  ];

  for (const [payload, expectedCode] of cases) {
    const decision = acl.evaluatePublish(
      edge,
      "spBv1.0/plant-a/NDEATH/node-1",
      payload,
    );
    assert.equal(decision.allowed, false);
    assert.equal(decision.code, expectedCode);
  }
});

test("protobuf NBIRTH and NDEATH payloads are checked", () => {
  const acl = new SparkplugAcl();

  assert.equal(
    acl.canPublish(edge, "spBv1.0/plant-a/NBIRTH/node-1", protobufBirth(42n)),
    true,
  );
  assert.equal(
    acl.canPublish(edge, "spBv1.0/plant-a/NDEATH/node-1", protobufBirth(42n)),
    true,
  );

  const forged = acl.evaluatePublish(
    edge,
    "spBv1.0/plant-a/NDEATH/node-1",
    protobufBirth(41n),
  );
  assert.equal(forged.allowed, false);
  assert.equal(forged.code, ACL_REASONS.DEATH_SEQUENCE_MISMATCH);
});

test("JSON-encoded Buffer payloads are supported", () => {
  const acl = new SparkplugAcl();
  const encodedBirth = new TextEncoder().encode(JSON.stringify(birthPayload(9)));
  const encodedDeath = new TextEncoder().encode(JSON.stringify(deathPayload(9)));

  assert.equal(acl.canPublish(edge, "spBv1.0/plant-a/NBIRTH/node-1", encodedBirth), true);
  assert.equal(acl.canPublish(edge, "spBv1.0/plant-a/NDEATH/node-1", encodedDeath), true);
});

test("Rebirth commands are limited to three per rolling minute per node", () => {
  const acl = new SparkplugAcl();
  const topic = "spBv1.0/plant-a/NCMD/node-1";

  for (const now of [0, 1_000, 2_000]) {
    assert.equal(acl.canPublish(app, topic, rebirthPayload(), { now }), true);
  }

  const flood = acl.evaluatePublish(app, topic, rebirthPayload(), { now: 3_000 });
  assert.equal(flood.allowed, false);
  assert.equal(flood.code, ACL_REASONS.REBIRTH_RATE_LIMITED);

  // A different node has an independent quota.
  assert.equal(
    acl.canPublish(app, "spBv1.0/plant-a/NCMD/node-2", rebirthPayload(), {
      now: 3_500,
    }),
    true,
  );

  // After the window, the old timestamps age out.
  assert.equal(acl.canPublish(app, topic, rebirthPayload(), { now: 62_001 }), true);
});

test("non-Rebirth commands do not consume the Rebirth quota", () => {
  const acl = new SparkplugAcl();
  const topic = "spBv1.0/plant-a/NCMD/node-1";

  for (let index = 0; index < 10; index += 1) {
    assert.equal(
      acl.canPublish(app, topic, { metrics: [{ name: "Node Control/Scan" }] }, {
        now: index,
      }),
      true,
    );
  }
  assert.equal(acl.canPublish(app, topic, rebirthPayload(), { now: 20 }), true);
  assert.equal(acl.canPublish(app, topic, rebirthPayload(), { now: 21 }), true);
  assert.equal(acl.canPublish(app, topic, rebirthPayload(), { now: 22 }), true);
  assert.equal(acl.canPublish(app, topic, rebirthPayload(), { now: 23 }), false);
});

test("Rebirth command detection supports protobuf and ignores false values", () => {
  assert.equal(isRebirthCommand(protobufRebirth(true)), true);
  assert.equal(isRebirthCommand(protobufRebirth(false)), false);
  assert.equal(isRebirthCommand(rebirthPayload(false)), false);

  const acl = new SparkplugAcl();
  const topic = "spBv1.0/plant-a/NCMD/node-1";
  for (let index = 0; index < 4; index += 1) {
    assert.equal(
      acl.canPublish(app, topic, protobufRebirth(false), { now: index }),
      true,
    );
  }
});

test("edge nodes cannot subscribe to command topics or use wildcards", () => {
  const acl = new SparkplugAcl();

  for (const filter of [
    "spBv1.0/plant-a/NCMD/node-1",
    "spBv1.0/plant-a/DCMD/node-1/device-1",
    "spBv1.0/plant-a/+/node-1",
    "spBv1.0/plant-a/#",
  ]) {
    const decision = acl.evaluateSubscribe(edge, filter);
    assert.equal(decision.allowed, false);
  }

  assert.equal(acl.canSubscribe(app, "spBv1.0/plant-a/NCMD/node-1"), true);
  assert.equal(acl.canSubscribe(app, "spBv1.0/#"), true);
  assert.equal(
    acl.canSubscribe(edge, "spBv1.0/plant-a/NDATA/node-1"),
    true,
  );
  assert.equal(
    acl.canSubscribe(edge, "spBv1.0/plant-a/NBIRTH/node-1"),
    true,
  );
  assert.equal(
    acl.canSubscribe(edge, "spBv1.0/plant-a/NDATA/node-2"),
    false,
  );
});

test("payload helpers extract bdSeq and match MQTT filters", () => {
  assert.deepEqual(extractBdSeq(12), { ok: true, bdSeq: 12n });
  assert.deepEqual(extractBdSeq("0x2a"), { ok: true, bdSeq: 42n });
  assert.deepEqual(extractBdSeq(birthPayload(0)), { ok: true, bdSeq: 0n });
  assert.equal(topicMatchesFilter("spBv1.0/plant-a/NDATA/node-1", "spBv1.0/+/NDATA/+"), true);
  assert.equal(topicMatchesFilter("spBv1.0/plant-a/NDATA/node-1", "spBv1.0/plant-a/NCMD/+"), false);
});

test("broker-style principal aliases and implicit JSON metric types work", () => {
  const acl = new SparkplugAcl();
  const aliasEdge = {
    type: "edge-node",
    clientid: "node-1",
    group_id: "plant-a",
  };

  assert.equal(
    acl.canPublish(
      aliasEdge,
      "spBv1.0/plant-a/NBIRTH/node-1",
      { metrics: [{ name: "bdSeq", dataType: "Int64", value: 5 }] },
    ),
    true,
  );
  assert.equal(
    acl.canPublish(
      aliasEdge,
      "spBv1.0/plant-a/NDEATH/node-1",
      { metrics: [{ name: "bdSeq", value: 5 }] },
    ),
    true,
  );
  assert.equal(
    isRebirthCommand({
      metrics: [{ name: "Node Control/Rebirth", value: true }],
    }),
    true,
  );
});

test("invalid principals are denied rather than guessed", () => {
  const acl = new SparkplugAcl();
  assert.equal(
    acl.canPublish({ kind: "anonymous", clientId: "x" }, "spBv1.0/a/NCMD/n"),
    false,
  );
  assert.equal(
    acl.canPublish({ kind: "edge-node", clientId: "node-1" }, "spBv1.0/a/NBIRTH/node-1", birthPayload(1)),
    false,
  );
  assert.equal(
    acl.canPublish(
      { kind: "edge-node", clientId: "node-1", nodeId: "node-2", groupId: "a" },
      "spBv1.0/a/NBIRTH/node-2",
      birthPayload(1),
    ),
    false,
  );
});
