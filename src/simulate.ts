import {
  AcceptanceController,
  InMemoryStagingStore,
  sha256Hex,
  type ArtifactManifest,
} from "./acceptance.ts";

const INPUT_DIGEST = sha256Hex("task-input-v1");
const TOOL_VERSION = "remote-exec/2.4.1";

interface FakeRun {
  manifest: ArtifactManifest;
  payloads: Map<string, Buffer>;
}

function buildRun(attempt: number, overrides?: { inputDigest?: string; toolVersion?: string }): FakeRun {
  const bodies = [
    `attempt=${attempt} chunk-0 payload`,
    `attempt=${attempt} chunk-1 payload`,
    `attempt=${attempt} chunk-2 payload`,
  ].map((text) => Buffer.from(text));
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
    artifactDigest: sha256Hex(Buffer.concat(bodies)),
    chunks,
  };
  const payloads = new Map<string, Buffer>();
  chunks.forEach((chunk, index) => payloads.set(chunk.id, bodies[index]));
  return { manifest, payloads };
}

function corrupt(data: Buffer): Buffer {
  const copy = Buffer.from(data);
  copy[0] = copy[0] ^ 0xff;
  return copy;
}

function showTransitions(controller: AcceptanceController, since: number): void {
  for (const transition of controller.transitions.slice(since)) {
    console.log(
      `    [attempt ${transition.attempt}] ${transition.from} -> ${transition.to}  (${transition.reason})`,
    );
  }
}

function scenario(title: string, fn: () => void): void {
  console.log(`\n=== ${title} ===`);
  fn();
}

scenario("1. 正常验收：清单匹配 -> 补取 -> 接受", () => {
  const controller = new AcceptanceController({ inputDigest: INPUT_DIGEST, toolVersion: TOOL_VERSION });
  const run = buildRun(1);
  const result = controller.submitManifest(run.manifest);
  console.log("  提交清单:", JSON.stringify(result));
  controller.fetchMissing((chunkId) => run.payloads.get(chunkId)!);
  showTransitions(controller, 0);
  const accepted = controller.accepted();
  console.log(`  验收结果: phase=${controller.snapshot().phase}, acceptedAttempt=${accepted?.attempt}`);
});

scenario("2. 重复清单：同代次同内容清单幂等去重", () => {
  const controller = new AcceptanceController({ inputDigest: INPUT_DIGEST, toolVersion: TOOL_VERSION });
  const run = buildRun(1);
  console.log("  第一次提交:", controller.submitManifest(run.manifest).status);
  console.log("  重复提交 1:", controller.submitManifest(run.manifest).status);
  console.log("  重复提交 2:", controller.submitManifest({ ...run.manifest }).status);
  const conflicting = { ...run.manifest, artifactDigest: sha256Hex("tampered") };
  console.log("  同代次不同内容:", JSON.stringify(controller.submitManifest(conflicting)));
  console.log("  转换记录条数(应只有 1 条 NONE->FETCHING):", controller.transitions.length);
});

scenario("3. 缺块补取：部分到达 -> 报告缺失 -> 补取完成", () => {
  const controller = new AcceptanceController({ inputDigest: INPUT_DIGEST, toolVersion: TOOL_VERSION });
  const run = buildRun(1);
  controller.submitManifest(run.manifest);
  const first = controller.deliverChunk(1, "chunk-0", run.payloads.get("chunk-0")!);
  console.log("  到达 chunk-0, 剩余缺失:", JSON.stringify(first));
  console.log("  missingChunks():", controller.missingChunks());
  controller.fetchMissing((chunkId) => run.payloads.get(chunkId)!);
  showTransitions(controller, 0);
  console.log("  最终状态:", controller.snapshot().phase);
});

scenario("4. 校验失败清理：坏块 -> 清除重取 -> 接受", () => {
  const store = new InMemoryStagingStore();
  const controller = new AcceptanceController({
    inputDigest: INPUT_DIGEST,
    toolVersion: TOOL_VERSION,
    store,
  });
  const run = buildRun(1);
  controller.submitManifest(run.manifest);
  const bad = controller.deliverChunk(1, "chunk-1", corrupt(run.payloads.get("chunk-1")!));
  console.log("  投递损坏 chunk-1:", bad.status, "(已清除该块, 重新计为缺失)");
  console.log("  暂存区键:", store.keys(), "缺失:", controller.missingChunks());
  controller.fetchMissing((chunkId) => run.payloads.get(chunkId)!);
  showTransitions(controller, 0);
  console.log("  最终状态:", controller.snapshot().phase, "坏块次数:", controller.snapshot().chunkChecksumFailures);
});

scenario("5. 整体摘要不匹配：清理暂存 -> 重试耗尽 -> FAILED", () => {
  const store = new InMemoryStagingStore();
  const controller = new AcceptanceController({
    inputDigest: INPUT_DIGEST,
    toolVersion: TOOL_VERSION,
    maxVerifyRetries: 2,
    store,
  });
  const run = buildRun(1);
  const poisoned: ArtifactManifest = { ...run.manifest, artifactDigest: sha256Hex("not-the-artifact") };
  controller.submitManifest(poisoned);
  for (let round = 1; round <= 3 && controller.snapshot().phase === "FETCHING"; round += 1) {
    console.log(`  第 ${round} 轮补取, 暂存键:`, store.keys());
    controller.fetchMissing((chunkId) => run.payloads.get(chunkId)!);
    console.log(`  第 ${round} 轮校验后: phase=${controller.snapshot().phase}, 暂存键:`, store.keys());
  }
  showTransitions(controller, 0);
});

scenario("6. 代次防护：旧尝试成功不能覆盖已接受的新尝试", () => {
  const controller = new AcceptanceController({ inputDigest: INPUT_DIGEST, toolVersion: TOOL_VERSION });
  const attempt2 = buildRun(2);
  controller.submitManifest(attempt2.manifest);
  controller.fetchMissing((chunkId) => attempt2.payloads.get(chunkId)!);
  console.log("  已接受代次:", controller.accepted()?.attempt);

  const attempt1 = buildRun(1);
  console.log("  旧代次清单(attempt 1):", JSON.stringify(controller.submitManifest(attempt1.manifest)));
  console.log("  旧代次数据块(attempt 1):", JSON.stringify(controller.deliverChunk(1, "chunk-0", attempt1.payloads.get("chunk-0")!)));
  console.log("  重复同代次清单(attempt 2):", JSON.stringify(controller.submitManifest(attempt2.manifest)));
  console.log("  验收记录仍为 attempt:", controller.accepted()?.attempt);

  const attempt3 = buildRun(3);
  console.log("  更新代次清单(attempt 3):", controller.submitManifest(attempt3.manifest).status);
  controller.fetchMissing((chunkId) => attempt3.payloads.get(chunkId)!);
  console.log("  验收记录推进到 attempt:", controller.accepted()?.attempt);
  showTransitions(controller, 0);
});

scenario("7. 输入摘要 / 工具版本不匹配的清单直接拒绝", () => {
  const controller = new AcceptanceController({ inputDigest: INPUT_DIGEST, toolVersion: TOOL_VERSION });
  const wrongInput = buildRun(1, { inputDigest: sha256Hex("other-input") });
  const wrongTool = buildRun(1, { toolVersion: "remote-exec/9.9.9" });
  console.log("  输入摘要不匹配:", JSON.stringify(controller.submitManifest(wrongInput.manifest)));
  console.log("  工具版本不匹配:", JSON.stringify(controller.submitManifest(wrongTool.manifest)));
  console.log("  控制器状态:", controller.snapshot().phase, "(保持 IDLE)");
});
