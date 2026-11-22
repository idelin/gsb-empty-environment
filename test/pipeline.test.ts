import assert from "node:assert/strict";
import test from "node:test";
import {
  PipelineError,
  createPipeline,
  encodeDeterministic,
  partitionTransport,
} from "../src/index.ts";
import { fixture, text, utf8 } from "./helpers.ts";

test("read callbacks are invisible until all verification completes", async () => {
  const { manifest, transport } = fixture({ decoded: text("publish me") });
  const pipeline = createPipeline(manifest);
  const reads: Uint8Array[] = [];
  pipeline.onRead((credential) => reads.push(credential.read()));

  pipeline.addShard(1, transport.slice(1));
  assert.equal(reads.length, 0);
  pipeline.addShard(0, transport.slice(0, 1));
  assert.equal(reads.length, 0);

  const asset = await pipeline.end();
  assert.equal(reads.length, 1);
  assert.equal(utf8(reads[0]), "publish me");
  assert.equal(utf8(asset.credential.read()), "publish me");
  assert.equal(asset.credential.byteLength, asset.bytes.byteLength);
});

test("digest failure aborts publication and ignores late callbacks", async () => {
  const { manifest, transport } = fixture({ decoded: text("bad digest") });
  const tampered = structuredClone(manifest);
  tampered.transport.groups[0].entries[0].digest = "0".repeat(64);
  const pipeline = createPipeline(tampered);
  const reads: string[] = [];
  pipeline.onRead(() => reads.push("pre"));
  pipeline.addShard(0, transport);

  await assert.rejects(
    pipeline.end(),
    (error: unknown) =>
      error instanceof PipelineError && error.code === "DIGEST_MISMATCH",
  );
  assert.equal(reads.length, 0);
  pipeline.onRead(() => reads.push("late"));
  const lateShard = pipeline.addShard(1, text("late"));
  assert.equal(lateShard.status, "ignored");
  await assert.rejects(
    pipeline.end(),
    (error: unknown) =>
      error instanceof PipelineError && error.code === "DIGEST_MISMATCH",
  );
  assert.equal(reads.length, 0);
});

test("duplicate shard indices conflict unless the bytes are identical", async () => {
  const { manifest, transport } = fixture({
    decoded: text("shard conflict"),
    shards: 2,
  });
  const pipeline = createPipeline(manifest);
  pipeline.addShard(0, new Uint8Array([1, 2, 3]));
  assert.equal(
    pipeline.addShard(0, new Uint8Array([1, 2, 3])).status,
    "duplicate",
  );
  assert.throws(
    () => pipeline.addShard(0, new Uint8Array([1, 2, 4])),
    (error: unknown) =>
      error instanceof PipelineError && error.code === "SHARD_CONFLICT",
  );
  assert.equal(pipeline.state, "failed");
});

test("disconnect before completion commits nothing", () => {
  const { manifest, transport } = fixture({
    decoded: text("disconnect partial"),
    shards: 2,
  });
  const pipeline = createPipeline(manifest);
  pipeline.addShard(0, transport.slice(0, 5));
  assert.equal(pipeline.disconnect(), null);
  assert.equal(pipeline.state, "cancelled");
});

test("disconnect after an atomic complete object returns its credential", async () => {
  const decoded = text("disconnect complete");
  const { manifest, transport } = fixture({
    decoded,
    shards: 2,
  });
  const pieces = partitionTransport(transport, [
    Math.floor(transport.byteLength / 2),
    transport.byteLength - Math.floor(transport.byteLength / 2),
  ]);
  const pipeline = createPipeline(manifest);
  pipeline.addShard(1, pieces[1]);
  pipeline.addShard(0, pieces[0]);
  const credential = pipeline.disconnect();
  assert.ok(credential);
  assert.equal(utf8(credential.read()), "disconnect complete");
  assert.equal(pipeline.state, "published");
  const asset = await pipeline.end();
  assert.equal(asset.credential, credential);
});

test("cancellation suppresses callbacks and rejects end", async () => {
  const { manifest, transport } = fixture({ decoded: text("cancel me") });
  const pipeline = createPipeline(manifest);
  let called = false;
  pipeline.onRead(() => {
    called = true;
  });
  pipeline.addShard(0, transport);
  pipeline.cancel("caller aborted");
  await assert.rejects(
    pipeline.end(),
    (error: unknown) =>
      error instanceof PipelineError && error.code === "CANCELLED",
  );
  assert.equal(called, false);
});

test("pipeline signals backpressure but can still finish atomically", async () => {
  const decoded = text("backpressure ".repeat(30));
  const { manifest, transport } = fixture({ decoded });
  const pipeline = createPipeline(manifest, {
    maxStagedBytes: 8,
    highWaterMark: 8,
  });
  const result = pipeline.addShard(0, transport);
  assert.equal(result.backpressured, true);
  assert.equal(pipeline.backpressured, true);
  const asset = await pipeline.end();
  assert.equal(utf8(asset.bytes), utf8(decoded));
});

test("missing shards fail with SHARD_GAP", async () => {
  const { manifest, transport } = fixture({
    decoded: text("gap"),
    shards: 2,
  });
  const pipeline = createPipeline(manifest);
  pipeline.addShard(1, transport);
  await assert.rejects(
    pipeline.end(),
    (error: unknown) =>
      error instanceof PipelineError && error.code === "SHARD_GAP",
  );
});

test("same transport bytes in different instances stay isolated", async () => {
  const raw = text("instance-TOKEN");
  const token = text("TOKEN");
  const { manifest, transport } = fixture({
    decoded: raw,
    placeholders: [
      {
        id: "token",
        offset: raw.byteLength - token.byteLength,
        length: token.byteLength,
        token,
        field: "requestId",
      },
    ],
  });
  const first = createPipeline(manifest, { identity: { requestId: "one" } });
  const second = createPipeline(manifest, { identity: { requestId: "two" } });
  first.addShard(0, transport);
  second.addShard(0, encodeDeterministic(raw));
  const [a, b] = await Promise.all([first.end(), second.end()]);
  assert.equal(utf8(a.bytes), "instance-one");
  assert.equal(utf8(b.bytes), "instance-two");
  assert.notEqual(a.credential, b.credential);
  assert.notEqual(a.credential.digest, b.credential.digest);
});
