import { strict as assert } from 'assert';
import { AcceptanceController } from '../src/acceptance';
import { chunkData, fileSpecFromContent, makeContext, makeManifest } from '../src/fixtures';
import { ContentStore } from '../src/store';
import type { ArtifactManifest } from '../src/types';

type TestFn = () => void;
const tests: Array<[string, TestFn]> = [];
function test(name: string, fn: TestFn): void {
  tests.push([name, fn]);
}

function acceptedController(attempt: number): {
  controller: AcceptanceController;
  store: ContentStore;
  manifest: ArtifactManifest;
} {
  const ctx = makeContext(attempt);
  const store = new ContentStore();
  const controller = new AcceptanceController(ctx, store);
  const spec = fileSpecFromContent('out/a.txt', 'abcdefgh');
  const contents = new Map([['out/a.txt', 'abcdefgh']]);
  const manifest = makeManifest(ctx, [spec]);
  controller.submitManifest(manifest);
  for (const chunk of spec.chunks) {
    controller.supplyChunk('out/a.txt', chunk.index, chunkData(contents, 'out/a.txt', chunk.index));
  }
  return { controller, store, manifest };
}

test('接受上下文完全匹配的清单并完成验收', () => {
  const { controller } = acceptedController(1);
  assert.equal(controller.getState(), 'accepted');
  assert.equal(controller.getAcceptedResult()?.attempt, 1);
});

test('拒绝输入摘要不匹配的清单', () => {
  const ctx = makeContext(1);
  const controller = new AcceptanceController(ctx);
  const receipt = controller.submitManifest(
    makeManifest(ctx, [], { inputDigest: 'tampered' }),
  );
  assert.equal(receipt.ok, false);
  assert.equal(receipt.reason, 'context-mismatch');
  assert.equal(controller.getState(), 'awaiting-manifest');
});

test('拒绝工具版本不匹配的清单', () => {
  const ctx = makeContext(1);
  const controller = new AcceptanceController(ctx);
  const receipt = controller.submitManifest(
    makeManifest(ctx, [], { toolVersion: 'worker/9.9.9' }),
  );
  assert.equal(receipt.ok, false);
  assert.equal(receipt.reason, 'context-mismatch');
});

test('拒绝代次不匹配的清单', () => {
  const ctx = makeContext(1);
  const controller = new AcceptanceController(ctx);
  const receipt = controller.submitManifest(makeManifest(ctx, [], { attempt: 2 }));
  assert.equal(receipt.ok, false);
  assert.equal(receipt.reason, 'context-mismatch');
});

test('重复清单幂等且不重置补取进度', () => {
  const ctx = makeContext(1);
  const controller = new AcceptanceController(ctx);
  const spec = fileSpecFromContent('out/a.txt', 'abcdefgh');
  const contents = new Map([['out/a.txt', 'abcdefgh']]);
  const manifest = makeManifest(ctx, [spec]);

  controller.submitManifest(manifest);
  controller.supplyChunk('out/a.txt', 0, chunkData(contents, 'out/a.txt', 0));
  const missingBefore = controller.missingChunks().length;

  const dup = controller.submitManifest(manifest);
  assert.equal(dup.ok, true);
  assert.equal(dup.reason, 'duplicate-manifest');
  assert.equal(controller.missingChunks().length, missingBefore);
  assert.equal(controller.getState(), 'fetching');
});

test('同代次但内容不同的清单被拒绝', () => {
  const ctx = makeContext(1);
  const controller = new AcceptanceController(ctx);
  controller.submitManifest(makeManifest(ctx, [fileSpecFromContent('out/a.txt', 'aaaa')]));
  const other = makeManifest(ctx, [fileSpecFromContent('out/b.txt', 'bbbb')]);
  const receipt = controller.submitManifest(other);
  assert.equal(receipt.ok, false);
  assert.equal(receipt.reason, 'context-mismatch');
});

test('缺块进入补取循环，齐套后自动验收', () => {
  const ctx = makeContext(1);
  const controller = new AcceptanceController(ctx);
  const spec = fileSpecFromContent('out/a.txt', '0123456789');
  const contents = new Map([['out/a.txt', '0123456789']]);
  controller.submitManifest(makeManifest(ctx, [spec]));

  assert.equal(controller.getState(), 'fetching');
  assert.equal(controller.missingChunks().length, spec.chunks.length);

  controller.supplyChunk('out/a.txt', 0, chunkData(contents, 'out/a.txt', 0));
  assert.equal(controller.getState(), 'fetching');
  assert.equal(controller.missingChunks().length, spec.chunks.length - 1);

  for (const ref of controller.missingChunks()) {
    controller.supplyChunk(ref.path, ref.index, chunkData(contents, ref.path, ref.index));
  }
  assert.equal(controller.getState(), 'accepted');
});

test('校验失败后清理该代次全部内容并进入终态', () => {
  const ctx = makeContext(1);
  const store = new ContentStore();
  const controller = new AcceptanceController(ctx, store);
  const spec = fileSpecFromContent('out/a.txt', 'abcdefgh');
  controller.submitManifest(makeManifest(ctx, [spec]));

  for (const chunk of spec.chunks) {
    controller.supplyChunk('out/a.txt', chunk.index, Buffer.from('XXXX'));
  }
  assert.equal(controller.getState(), 'failed');
  assert.match(controller.getFailReason() ?? '', /checksum mismatch/);
  assert.equal(store.count(ctx.attempt), 0);

  // 终态下继续推送分块被拒绝。
  const late = controller.supplyChunk('out/a.txt', 0, Buffer.from('abcd'));
  assert.equal(late.ok, false);
  assert.equal(late.reason, 'unexpected-state');
});

test('失败后升级代次可重新验收', () => {
  const store = new ContentStore();
  const controller = new AcceptanceController(makeContext(1), store);
  const spec = fileSpecFromContent('out/a.txt', 'abcdefgh');
  controller.submitManifest(makeManifest(makeContext(1), [spec]));
  for (const chunk of spec.chunks) {
    controller.supplyChunk('out/a.txt', chunk.index, Buffer.from('XXXX'));
  }
  assert.equal(controller.getState(), 'failed');

  controller.expectAttempt(makeContext(2));
  assert.equal(controller.getState(), 'awaiting-manifest');
  const ctx2 = makeContext(2);
  const contents = new Map([['out/a.txt', 'abcdefgh']]);
  controller.submitManifest(makeManifest(ctx2, [spec]));
  for (const chunk of spec.chunks) {
    controller.supplyChunk('out/a.txt', chunk.index, chunkData(contents, 'out/a.txt', chunk.index));
  }
  assert.equal(controller.getState(), 'accepted');
  assert.equal(controller.getAcceptedResult()?.attempt, 2);
});

test('旧代次的迟到清单与成功回报不得覆盖已接受的新代次', () => {
  const { controller, manifest: staleManifest } = acceptedController(2);

  const stale = controller.submitManifest({ ...staleManifest, attempt: 1 });
  assert.equal(stale.ok, false);
  assert.equal(stale.reason, 'stale-attempt');
  assert.equal(controller.notifyRemoteSuccess(1), false);

  assert.equal(controller.getState(), 'accepted');
  assert.equal(controller.getAcceptedResult()?.attempt, 2);
});

test('不允许代次回退', () => {
  const { controller } = acceptedController(3);
  assert.throws(() => controller.expectAttempt(makeContext(2)), /regress/);
});

test('未知分块被拒绝', () => {
  const ctx = makeContext(1);
  const controller = new AcceptanceController(ctx);
  controller.submitManifest(makeManifest(ctx, [fileSpecFromContent('out/a.txt', 'abcdefgh')]));
  const receipt = controller.supplyChunk('out/ghost.txt', 0, Buffer.from('abcd'));
  assert.equal(receipt.ok, false);
  assert.equal(receipt.reason, 'unknown-chunk');
});

let failed = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    console.log(`ok   - ${name}`);
  } catch (err) {
    failed += 1;
    console.error(`FAIL - ${name}`);
    console.error(err);
  }
}
if (failed > 0) {
  console.error(`${failed} test(s) failed`);
  process.exit(1);
}
console.log(`${tests.length} tests passed`);
