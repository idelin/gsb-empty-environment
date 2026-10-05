import assert from "node:assert/strict";
import test from "node:test";
import {
  AcceptanceController,
  InMemoryStagingStore,
  sha256Hex,
  type ArtifactManifest,
} from "../src/acceptance.ts";

const INPUT_DIGEST = sha256Hex("task-input-v1");
const TOOL_VERSION = "remote-exec/2.4.1";

function buildRun(
  attempt: number,
  overrides?: { inputDigest?: string; toolVersion?: string; artifactDigest?: string },
): { manifest: ArtifactManifest; payloads: Map<string, Buffer> } {
  const bodies = [0, 1, 2].map((index) => Buffer.from(`attempt=${attempt} chunk-${index}`));
  const chunks = bodies.map((body, index) => ({
    id: `chunk-${index}`,
    sha256: sha256Hex(body),
    size: body.length,
  }));
  const manifest: ArtifactManifest = {
    runId: "run-42",
    inputDigest: overrides?.inputDigest ?? INPUT_DIGEST,
    toolVersion: overrides?.toolVersion ?? TOOL_VERSION,
    attempt,
    artifactDigest: overrides?.artifactDigest ?? sha256Hex(Buffer.concat(bodies)),
    chunks,
  };
  const payloads = new Map<string, Buffer>();
  chunks.forEach((chunk, index) => payloads.set(chunk.id, bodies[index]));
  return { manifest, payloads };
}

function makeController(options?: { maxVerifyRetries?: number; store?: InMemoryStagingStore }) {
  return new AcceptanceController({
    inputDigest: INPUT_DIGEST,
    toolVersion: TOOL_VERSION,
    maxVerifyRetries: options?.maxVerifyRetries,
    store: options?.store,
  });
}

test("接受输入摘要、工具版本、代次都匹配的清单并验收", () => {
  const controller = makeController();
  const run = buildRun(1);
  const result = controller.submitManifest(run.manifest);
  assert.equal(result.status, "TRACKING");
  controller.fetchMissing((chunkId) => run.payloads.get(chunkId)!);
  assert.equal(controller.snapshot().phase, "ACCEPTED");
  assert.equal(controller.accepted()?.attempt, 1);
  assert.deepEqual(
    controller.transitions.map((transition) => transition.to),
    ["FETCHING", "VERIFYING", "ACCEPTED"],
  );
});

test("拒绝输入摘要或工具版本不匹配的清单", () => {
  const controller = makeController();
  const wrongInput = buildRun(1, { inputDigest: sha256Hex("other") });
  const wrongTool = buildRun(1, { toolVersion: "remote-exec/0.0.1" });
  assert.deepEqual(controller.submitManifest(wrongInput.manifest), {
    status: "REJECTED",
    reason: "INPUT_DIGEST_MISMATCH",
  });
  assert.deepEqual(controller.submitManifest(wrongTool.manifest), {
    status: "REJECTED",
    reason: "TOOL_VERSION_MISMATCH",
  });
  assert.equal(controller.snapshot().phase, "IDLE");
});

test("重复清单幂等去重，同代次冲突清单被拒绝", () => {
  const controller = makeController();
  const run = buildRun(1);
  assert.equal(controller.submitManifest(run.manifest).status, "TRACKING");
  assert.equal(controller.submitManifest(run.manifest).status, "DUPLICATE");
  assert.equal(controller.submitManifest({ ...run.manifest }).status, "DUPLICATE");
  const tampered = { ...run.manifest, artifactDigest: sha256Hex("tampered") };
  assert.deepEqual(controller.submitManifest(tampered), {
    status: "REJECTED",
    reason: "MANIFEST_CONFLICT",
  });
  assert.equal(controller.transitions.length, 1);
});

test("缺块被报告并可通过补取完成", () => {
  const controller = makeController();
  const run = buildRun(1);
  controller.submitManifest(run.manifest);
  controller.deliverChunk(1, "chunk-0", run.payloads.get("chunk-0")!);
  assert.deepEqual(controller.missingChunks(), ["chunk-1", "chunk-2"]);
  controller.fetchMissing((chunkId) => run.payloads.get(chunkId)!);
  assert.equal(controller.snapshot().phase, "ACCEPTED");
});

test("块校验失败清除该块并重新计为缺失", () => {
  const store = new InMemoryStagingStore();
  const controller = makeController({ store });
  const run = buildRun(1);
  controller.submitManifest(run.manifest);
  const corrupted = Buffer.from("garbage-garbage-garbage!!");
  const result = controller.deliverChunk(1, "chunk-1", corrupted);
  assert.equal(result.status, "CHECKSUM_MISMATCH");
  assert.deepEqual(store.keys(), []);
  assert.deepEqual(controller.missingChunks(), ["chunk-0", "chunk-1", "chunk-2"]);
  controller.fetchMissing((chunkId) => run.payloads.get(chunkId)!);
  assert.equal(controller.snapshot().phase, "ACCEPTED");
  assert.equal(controller.snapshot().chunkChecksumFailures, 1);
});

test("整体摘要不匹配触发清理与重试，耗尽后 FAILED", () => {
  const store = new InMemoryStagingStore();
  const controller = makeController({ maxVerifyRetries: 1, store });
  const run = buildRun(1, { artifactDigest: sha256Hex("wrong") });
  const payloads = buildRun(1).payloads;
  controller.submitManifest(run.manifest);
  controller.fetchMissing((chunkId) => payloads.get(chunkId)!);
  assert.equal(controller.snapshot().phase, "FETCHING");
  assert.deepEqual(store.keys(), []);
  assert.equal(controller.snapshot().verifyRetries, 1);
  controller.fetchMissing((chunkId) => payloads.get(chunkId)!);
  assert.equal(controller.snapshot().phase, "FAILED");
  assert.deepEqual(
    controller.transitions.map((transition) => transition.to),
    ["FETCHING", "VERIFYING", "CLEANING_UP", "FETCHING", "VERIFYING", "CLEANING_UP", "FAILED"],
  );
});

test("旧尝试不能覆盖已接受的新尝试", () => {
  const controller = makeController();
  const attempt2 = buildRun(2);
  controller.submitManifest(attempt2.manifest);
  controller.fetchMissing((chunkId) => attempt2.payloads.get(chunkId)!);
  assert.equal(controller.accepted()?.attempt, 2);

  const attempt1 = buildRun(1);
  assert.deepEqual(controller.submitManifest(attempt1.manifest), {
    status: "REJECTED",
    reason: "STALE_ATTEMPT",
  });
  assert.equal(
    controller.deliverChunk(1, "chunk-0", attempt1.payloads.get("chunk-0")!).status,
    "STALE_ATTEMPT",
  );
  assert.deepEqual(controller.submitManifest(attempt2.manifest), {
    status: "REJECTED",
    reason: "ATTEMPT_ALREADY_ACCEPTED",
  });
  assert.equal(controller.accepted()?.attempt, 2);
});

test("更新代次取代进行中的旧代次并清理其暂存", () => {
  const store = new InMemoryStagingStore();
  const controller = makeController({ store });
  const attempt1 = buildRun(1);
  controller.submitManifest(attempt1.manifest);
  controller.deliverChunk(1, "chunk-0", attempt1.payloads.get("chunk-0")!);
  assert.deepEqual(store.keys(), ["1/chunk-0"]);

  const attempt2 = buildRun(2);
  assert.equal(controller.submitManifest(attempt2.manifest).status, "TRACKING");
  assert.deepEqual(store.keys(), []);
  assert.equal(
    controller.deliverChunk(1, "chunk-1", attempt1.payloads.get("chunk-1")!).status,
    "STALE_ATTEMPT",
  );
  controller.fetchMissing((chunkId) => attempt2.payloads.get(chunkId)!);
  assert.equal(controller.accepted()?.attempt, 2);
});
