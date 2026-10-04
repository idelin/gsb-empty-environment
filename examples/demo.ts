import { DependencyExecutor } from '../src/executor.ts';

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

const executor = new DependencyExecutor({ concurrency: 4 });

executor.addNode('fetch', async () => {
  console.log('[fetch] start');
  await sleep(200);
  console.log('[fetch] done');
  return 'raw-data';
});
executor.addNode('parse', async () => 'parsed', ['fetch']);
executor.addNode('validate', async () => 'validated', ['parse']);
executor.addNode('report', async () => 'report', ['validate']);
executor.addNode('audit', async () => {
  await sleep(300);
  return 'audit';
}); // 与 fetch 无关的独立分支：已启动，保留自身结果

const runPromise = executor.run();

// fetch 运行 100ms 后撤销它：parse/validate/report 尚未启动，随之撤销；
// fetch 与 audit 已启动，跑完并保留结果。
setTimeout(() => {
  console.log('>> revoke(fetch) while it is running');
  executor.revoke('fetch', 'upstream deprecated');
}, 100);

const report = await runPromise;
for (const [id, result] of report.results) {
  const extra = result.revocation
    ? `chain=${result.revocation.chain.join(' -> ')} primary=${String(
        result.revocation.primaryCause,
      )}`
    : `value=${JSON.stringify(result.value)}`;
  console.log(`${id}: ${result.status} (${extra})`);
}
