import { AcceptanceController } from './acceptance';
import { chunkData, fileSpecFromContent, makeContext, makeManifest } from './fixtures';
import { ContentStore } from './store';

function log(msg: string): void {
  console.log(msg);
}

/** 场景 A：重复清单幂等，不重置补取进度。 */
function scenarioDuplicateManifest(): void {
  log('--- A. 重复清单 ---');
  const ctx = makeContext(1);
  const spec = fileSpecFromContent('out/result.txt', 'hello, acceptance!');
  const contents = new Map([['out/result.txt', 'hello, acceptance!']]);
  const controller = new AcceptanceController(ctx);

  const manifest = makeManifest(ctx, [spec]);
  const first = controller.submitManifest(manifest);
  log(`首次提交清单: ok=${first.ok}, state=${first.state}`);

  controller.supplyChunk('out/result.txt', 0, chunkData(contents, 'out/result.txt', 0));
  log(`已补 1 块，仍缺 ${controller.missingChunks().length} 块`);

  const dup = controller.submitManifest(manifest);
  log(`重复提交同一清单: ok=${dup.ok}, reason=${dup.reason}, state=${dup.state}`);
  log(`重复清单后仍缺 ${controller.missingChunks().length} 块（进度未被重置）`);
}

/** 场景 B：缺块补取循环直至验收。 */
function scenarioMissingChunks(): void {
  log('--- B. 缺块补取 ---');
  const ctx = makeContext(1);
  const spec = fileSpecFromContent('out/report.bin', '0123456789abcdef');
  const contents = new Map([['out/report.bin', '0123456789abcdef']]);
  const controller = new AcceptanceController(ctx);

  controller.submitManifest(makeManifest(ctx, [spec]));
  controller.supplyChunk('out/report.bin', 0, chunkData(contents, 'out/report.bin', 0));
  log(`首轮推送后缺 ${controller.missingChunks().length} 块，状态=${controller.getState()}`);

  // 补取循环：按 missingChunks 逐块拉取。
  let round = 0;
  while (controller.getState() === 'fetching') {
    round += 1;
    const missing = controller.missingChunks();
    log(`补取第 ${round} 轮：请求 ${missing.length} 块`);
    for (const ref of missing) {
      controller.supplyChunk(ref.path, ref.index, chunkData(contents, ref.path, ref.index));
    }
  }
  const result = controller.getAcceptedResult();
  log(`验收完成：state=${controller.getState()}, attempt=${result?.attempt}`);
}

/** 场景 C：校验失败后清理。 */
function scenarioChecksumFailure(): void {
  log('--- C. 校验失败与清理 ---');
  const ctx = makeContext(1);
  const spec = fileSpecFromContent('out/data.txt', 'good-data');
  const store = new ContentStore();
  const controller = new AcceptanceController(ctx, store);

  controller.submitManifest(makeManifest(ctx, [spec]));
  // 执行器推送了被篡改的分块。
  for (const chunk of spec.chunks) {
    controller.supplyChunk('out/data.txt', chunk.index, Buffer.from('EVIL'));
  }
  log(`校验结果: state=${controller.getState()}, reason=${controller.getFailReason()}`);
  log(`失败后残留分块数: ${store.count(ctx.attempt)}（已清理）`);

  // 升级代次后重新武装，可再次验收。
  const ctx2 = makeContext(2);
  controller.expectAttempt(ctx2);
  const spec2 = fileSpecFromContent('out/data.txt', 'good-data');
  const contents = new Map([['out/data.txt', 'good-data']]);
  controller.submitManifest(makeManifest(ctx2, [spec2]));
  for (const chunk of spec2.chunks) {
    controller.supplyChunk('out/data.txt', chunk.index, chunkData(contents, 'out/data.txt', chunk.index));
  }
  log(`代次 2 重试: state=${controller.getState()}`);
}

/** 场景 D：旧代次的迟到成功不得覆盖已接受的新代次。 */
function scenarioStaleAttempt(): void {
  log('--- D. 旧代次不得覆盖新代次 ---');
  const store = new ContentStore();
  const controller = new AcceptanceController(makeContext(1), store);

  // 代次 1 失败，升级到代次 2 并验收成功。
  controller.expectAttempt(makeContext(2));
  const ctx2 = makeContext(2);
  const spec = fileSpecFromContent('out/final.txt', 'final-result');
  const contents = new Map([['out/final.txt', 'final-result']]);
  controller.submitManifest(makeManifest(ctx2, [spec]));
  for (const chunk of spec.chunks) {
    controller.supplyChunk('out/final.txt', chunk.index, chunkData(contents, 'out/final.txt', chunk.index));
  }
  log(`代次 2 已验收: state=${controller.getState()}, attempt=${controller.getAcceptedResult()?.attempt}`);

  // 代次 1 的清单与成功回报迟到。
  const staleManifest = makeManifest(makeContext(1), [spec]);
  const stale = controller.submitManifest(staleManifest);
  const staleSuccess = controller.notifyRemoteSuccess(1);
  log(`旧清单: ok=${stale.ok}, reason=${stale.reason}`);
  log(`旧成功回报被采纳: ${staleSuccess}`);
  log(`当前验收结果仍为 attempt=${controller.getAcceptedResult()?.attempt}, state=${controller.getState()}`);
}

scenarioDuplicateManifest();
scenarioMissingChunks();
scenarioChecksumFailure();
scenarioStaleAttempt();
