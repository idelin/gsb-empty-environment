import { test } from "node:test";
import assert from "node:assert/strict";
import {
  SparkplugAcl,
  createSparkplugAcl,
  type AclDecision,
} from "./index.ts";

interface Credentials {
  clientId: string;
  username: string;
  password: string;
}

const app: Credentials = {
  clientId: "primary-app",
  username: "primary-app",
  password: "app-secret",
};

const edgeA: Credentials = {
  clientId: "edge-a",
  username: "edge-a",
  password: "edge-secret",
};

const edgeB: Credentials = {
  clientId: "edge-b",
  username: "edge-b",
  password: "edge-secret",
};

const edgeC: Credentials = {
  clientId: "edge-c",
  username: "edge-c",
  password: "edge-secret",
};

function makeAcl(): {
  acl: SparkplugAcl;
  setNow: (value: number) => void;
  advance: (value: number) => void;
} {
  let now = 0;
  const acl = new SparkplugAcl({
    application: app,
    edgeNodes: [
      { ...edgeA, groupId: "plant-a" },
      { ...edgeB, groupId: "plant-a" },
      { ...edgeC, groupId: "plant-b" },
    ],
    now: () => now,
  });
  return {
    acl,
    setNow(value: number) {
      now = value;
    },
    advance(value: number) {
      now += value;
    },
  };
}

function publish(
  acl: SparkplugAcl,
  who: Credentials,
  topic: string,
  payload?: unknown,
): AclDecision {
  return acl.authorize({
    action: "publish",
    ...who,
    topic,
    payload,
  });
}

function subscribe(
  acl: SparkplugAcl,
  who: Credentials,
  topic: string,
): AclDecision {
  return acl.authorize({
    action: "subscribe",
    ...who,
    topic,
  });
}

function bdSeq(value: number | bigint | string): {
  metrics: Array<{ name: string; value: number | bigint | string }>;
} {
  return { metrics: [{ name: "bdSeq", value }] };
}

const rebirth = {
  metrics: [{ name: "Node Control/Rebirth", value: true }],
};

const textEncoder = new TextEncoder();

function encodeVarint(value: bigint): number[] {
  const bytes: number[] = [];
  let remaining = value;
  do {
    let byte = Number(remaining & 0x7fn);
    remaining >>= 7n;
    if (remaining > 0n) {
      byte |= 0x80;
    }
    bytes.push(byte);
  } while (remaining > 0n);
  return bytes;
}

function varintField(fieldNumber: number, value: bigint): number[] {
  return [
    ...encodeVarint(BigInt((fieldNumber << 3) | 0)),
    ...encodeVarint(value),
  ];
}

function bytesField(fieldNumber: number, value: Uint8Array): number[] {
  return [
    ...encodeVarint(BigInt((fieldNumber << 3) | 2)),
    ...encodeVarint(BigInt(value.length)),
    ...Array.from(value),
  ];
}

function protobufMetric(
  name: string,
  valueField: number,
  value: bigint,
): Uint8Array {
  return Uint8Array.from([
    ...bytesField(1, textEncoder.encode(name)),
    ...varintField(valueField, value),
  ]);
}

function protobufPayload(metrics: Uint8Array[]): Uint8Array {
  const bytes: number[] = [];
  for (const metric of metrics) {
    bytes.push(...bytesField(2, metric));
  }
  return Uint8Array.from(bytes);
}

test("edge identities cannot publish or subscribe outside their command scope", () => {
  const { acl } = makeAcl();

  assert.equal(
    publish(acl, app, "spBv1.0/plant-a/NCMD/edge-a").allow,
    true,
  );
  assert.equal(
    publish(acl, app, "spBv1.0/plant-a/DCMD/edge-a/pump-1").allow,
    true,
  );

  const edgeNcmd = publish(acl, edgeA, "spBv1.0/plant-a/NCMD/edge-a");
  assert.equal(edgeNcmd.allow, false);
  assert.equal(edgeNcmd.reason, "topic-not-allowed");

  const edgeDcmd = publish(
    acl,
    edgeA,
    "spBv1.0/plant-a/DCMD/edge-a/pump-1",
  );
  assert.equal(edgeDcmd.allow, false);

  const appBirth = publish(acl, app, "spBv1.0/plant-a/NBIRTH/edge-a");
  assert.equal(appBirth.allow, false);

  assert.equal(
    publish(acl, edgeA, "spBv1.0/plant-a/NBIRTH/edge-a", bdSeq(1)).allow,
    true,
  );
  assert.equal(
    publish(acl, edgeA, "spBv1.0/plant-a/NDATA/edge-a", {
      metrics: [{ name: "temperature", value: 21.5 }],
    }).allow,
    true,
  );
  assert.equal(
    publish(acl, edgeA, "spBv1.0/plant-a/DDATA/edge-a/pump-1").allow,
    false,
  );

  assert.equal(
    publish(acl, edgeA, "spBv1.0/plant-b/NBIRTH/edge-a", bdSeq(1)).reason,
    "group-mismatch",
  );
  assert.equal(
    publish(acl, edgeA, "spBv1.0/plant-a/NBIRTH/edge-b", bdSeq(1)).reason,
    "node-mismatch",
  );
  assert.equal(
    publish(acl, edgeA, "spBv1.0/plant-a/NBIRTH/+", bdSeq(1)).reason,
    "invalid-topic",
  );

  const spoofedClient = publish(
    acl,
    edgeA,
    "spBv1.0/plant-a/NBIRTH/edge-b",
    bdSeq(1),
  );
  assert.equal(spoofedClient.allow, false);

  const stolenClient = acl.authorize({
    action: "publish",
    clientId: "edge-b",
    username: edgeA.username,
    password: edgeA.password,
    topic: "spBv1.0/plant-a/NBIRTH/edge-b",
    payload: bdSeq(1),
  });
  assert.equal(stolenClient.reason, "client-id-mismatch");

  const wrongPassword = acl.authorize({
    action: "publish",
    clientId: app.clientId,
    username: app.username,
    password: "wrong",
    topic: "spBv1.0/plant-a/NCMD/edge-a",
  });
  assert.equal(wrongPassword.reason, "invalid-credential");

  const unknown = acl.authorize({
    action: "publish",
    clientId: "unknown",
    username: "unknown",
    password: "unknown",
    topic: "spBv1.0/plant-a/NBIRTH/unknown",
  });
  assert.equal(unknown.reason, "unknown-principal");

  assert.equal(
    subscribe(acl, edgeA, "spBv1.0/plant-a/NCMD/edge-a").allow,
    true,
  );
  assert.equal(
    subscribe(acl, edgeA, "spBv1.0/plant-a/DCMD/edge-a/+").allow,
    true,
  );
  assert.equal(
    subscribe(acl, edgeA, "spBv1.0/plant-a/DCMD/edge-a/#").allow,
    true,
  );
  assert.equal(
    subscribe(acl, edgeA, "spBv1.0/plant-a/NCMD/edge-b").allow,
    false,
  );
  assert.equal(
    subscribe(acl, edgeA, "spBv1.0/plant-a/NCMD/+").allow,
    false,
  );
  assert.equal(
    subscribe(acl, edgeA, "spBv1.0/+/NCMD/edge-a").allow,
    false,
  );
  assert.equal(subscribe(acl, edgeA, "spBv1.0/plant-a/#").allow, false);
  assert.equal(
    subscribe(acl, edgeA, "spBv1.0/plant-a/NDATA/edge-a").allow,
    false,
  );
  assert.equal(
    subscribe(acl, edgeA, "$share/group/spBv1.0/plant-a/NCMD/edge-a").allow,
    false,
  );
  assert.equal(subscribe(acl, app, "spBv1.0/#").allow, true);
  assert.equal(subscribe(acl, app, "#").allow, false);
});

test("NDEATH is bound to the latest NBIRTH bdSeq and cannot be forged", () => {
  const { acl } = makeAcl();
  const birthTopic = "spBv1.0/plant-a/NBIRTH/edge-a";
  const deathTopic = "spBv1.0/plant-a/NDEATH/edge-a";

  assert.equal(
    publish(acl, edgeA, deathTopic, bdSeq(0)).reason,
    "birth-required",
  );
  assert.equal(publish(acl, edgeA, birthTopic, bdSeq(7)).allow, true);
  assert.equal(acl.lastBdSeq("plant-a", "edge-a"), "7");

  assert.equal(
    publish(acl, edgeA, deathTopic, bdSeq(8)).reason,
    "bdseq-mismatch",
  );
  assert.equal(
    publish(acl, edgeA, deathTopic, {}).reason,
    "invalid-bdseq",
  );
  assert.equal(
    publish(acl, edgeA, deathTopic, { metrics: [] }).reason,
    "invalid-bdseq",
  );
  assert.equal(publish(acl, edgeA, deathTopic, bdSeq(7)).allow, true);
  assert.equal(
    publish(acl, edgeA, deathTopic, JSON.stringify(bdSeq("7"))).allow,
    true,
  );

  assert.equal(
    publish(acl, edgeB, deathTopic, bdSeq(7)).reason,
    "node-mismatch",
  );
  const forgedByOtherNode = acl.authorize({
    action: "publish",
    clientId: "edge-a",
    username: edgeB.username,
    password: edgeB.password,
    topic: deathTopic,
    payload: bdSeq(7),
  });
  assert.equal(forgedByOtherNode.reason, "client-id-mismatch");

  assert.equal(publish(acl, edgeA, birthTopic, bdSeq(9)).allow, true);
  assert.equal(
    publish(acl, edgeA, deathTopic, bdSeq(7)).reason,
    "bdseq-mismatch",
  );
  assert.equal(publish(acl, edgeA, deathTopic, bdSeq(9)).allow, true);

  const conflictingBirth = publish(acl, edgeA, birthTopic, {
    metrics: [
      { name: "bdSeq", value: 10 },
      { name: "bdSeq", value: 11 },
    ],
  });
  assert.equal(conflictingBirth.reason, "invalid-bdseq");
  assert.equal(acl.lastBdSeq("plant-a", "edge-a"), "9");

  const binaryBirth = protobufPayload([
    protobufMetric("bdSeq", 10, 11n),
  ]);
  assert.equal(publish(acl, edgeA, birthTopic, binaryBirth).allow, true);
  assert.equal(acl.lastBdSeq("plant-a", "edge-a"), "11");

  const binaryDeath = protobufPayload([
    protobufMetric("bdSeq", 10, 11n),
  ]);
  assert.equal(publish(acl, edgeA, deathTopic, binaryDeath).allow, true);

  const binaryForgery = protobufPayload([
    protobufMetric("bdSeq", 10, 12n),
  ]);
  assert.equal(
    publish(acl, edgeA, deathTopic, binaryForgery).reason,
    "bdseq-mismatch",
  );

  const ambiguousDeath = publish(acl, edgeA, deathTopic, {
    bdSeq: 11,
    metrics: [{ name: "bdSeq", value: 12 }],
  });
  assert.equal(ambiguousDeath.reason, "invalid-bdseq");
  assert.equal(publish(acl, edgeA, deathTopic, bdSeq(11)).allow, true);

  const jsonBirthBytes = textEncoder.encode(JSON.stringify(bdSeq(13)));
  assert.equal(publish(acl, edgeA, birthTopic, jsonBirthBytes).allow, true);
  assert.equal(acl.lastBdSeq("plant-a", "edge-a"), "13");
  const jsonDeathBytes = textEncoder.encode(JSON.stringify(bdSeq(13)));
  assert.equal(publish(acl, edgeA, deathTopic, jsonDeathBytes).allow, true);
});

test("Rebirth commands are limited to three per node in a rolling minute", () => {
  const { acl, advance } = makeAcl();
  const topic = "spBv1.0/plant-a/NCMD/edge-a";

  assert.equal(publish(acl, app, topic, rebirth).allow, true);
  advance(1_000);
  assert.equal(publish(acl, app, topic, rebirth).allow, true);
  advance(1_000);
  assert.equal(publish(acl, app, topic, rebirth).allow, true);
  advance(1_000);

  const limited = publish(acl, app, topic, rebirth);
  assert.equal(limited.allow, false);
  assert.equal(limited.reason, "rebirth-rate-limited");
  assert.equal(limited.retryAfterMs, 57_000);

  assert.equal(
    publish(acl, app, topic, {
      metrics: [{ name: "Node Control/Scan", value: true }],
    }).allow,
    true,
  );
  assert.equal(publish(acl, app, topic, rebirth).allow, false);

  advance(57_000);
  assert.equal(publish(acl, app, topic, rebirth).allow, true);
  advance(1_000);
  assert.equal(publish(acl, app, topic, rebirth).allow, true);

  const otherNode = "spBv1.0/plant-a/NCMD/edge-b";
  assert.equal(publish(acl, app, otherNode, rebirth).allow, true);
  assert.equal(publish(acl, app, otherNode, rebirth).allow, true);
  assert.equal(publish(acl, app, otherNode, rebirth).allow, true);
  assert.equal(publish(acl, app, otherNode, rebirth).allow, false);

  const dcmd = "spBv1.0/plant-a/DCMD/edge-c/pump-1";
  assert.equal(publish(acl, app, dcmd, rebirth).allow, true);
  assert.equal(publish(acl, app, dcmd, rebirth).allow, true);
  assert.equal(publish(acl, app, dcmd, rebirth).allow, true);
  assert.equal(publish(acl, app, dcmd, rebirth).allow, false);
});

test("unauthorized Rebirth attempts do not consume the node quota", () => {
  const { acl } = makeAcl();
  const topic = "spBv1.0/plant-a/NCMD/edge-a";

  for (let index = 0; index < 5; index += 1) {
    assert.equal(publish(acl, edgeA, topic, rebirth).allow, false);
  }
  assert.equal(publish(acl, app, topic, rebirth).allow, true);
  assert.equal(publish(acl, app, topic, rebirth).allow, true);
  assert.equal(publish(acl, app, topic, rebirth).allow, true);
  assert.equal(publish(acl, app, topic, rebirth).allow, false);
});

test("Rebirth limits are configurable and binary commands are recognized", () => {
  let now = 0;
  const acl = createSparkplugAcl({
    application: app,
    edgeNodes: [
      { ...edgeA, groupId: "plant-a" },
      { ...edgeB, groupId: "plant-a" },
    ],
    rebirthLimit: 2,
    rebirthWindowMs: 5_000,
    now: () => now,
  });
  const topic = "spBv1.0/plant-a/NCMD/edge-a";
  const binaryRebirth = protobufPayload([
    protobufMetric("Node Control/Rebirth", 13, 1n),
  ]);

  assert.equal(publish(acl, app, topic, binaryRebirth).allow, true);
  assert.equal(publish(acl, app, topic, binaryRebirth).allow, true);
  assert.equal(publish(acl, app, topic, binaryRebirth).allow, false);
  now = 5_000;
  assert.equal(publish(acl, app, topic, binaryRebirth).allow, true);

  const jsonRebirth = textEncoder.encode(JSON.stringify(rebirth));
  const otherTopic = "spBv1.0/plant-a/NCMD/edge-b";
  assert.equal(publish(acl, app, otherTopic, jsonRebirth).allow, true);
  assert.equal(publish(acl, app, otherTopic, jsonRebirth).allow, true);
  assert.equal(publish(acl, app, otherTopic, jsonRebirth).allow, false);
});

test("malformed and unknown requests fail closed", () => {
  const { acl } = makeAcl();

  const badAction = acl.authorize({
    action: "drop",
    ...app,
    topic: "spBv1.0/plant-a/NCMD/edge-a",
  });
  assert.equal(badAction.reason, "invalid-request");

  const missingTopic = acl.authorize({
    action: "publish",
    ...app,
  } as never);
  assert.equal(missingTopic.reason, "invalid-topic");

  const wildcardPublish = publish(
    acl,
    app,
    "spBv1.0/plant-a/NCMD/+",
  );
  assert.equal(wildcardPublish.reason, "invalid-topic");

  const shortSubscription = subscribe(acl, edgeA, "spBv1.0/plant-a/#");
  assert.equal(shortSubscription.allow, false);
});
