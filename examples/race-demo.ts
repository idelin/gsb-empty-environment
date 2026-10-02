/**
 * 运行样例：复现 node-a / node-b 在同一物理毫秒争抢主租约的竞态，
 * 并用审计追踪器回放全过程、打印拒绝响应的因果证据链。
 *
 * 运行：node examples/race-demo.ts   （Node >= 23.6 原生支持 TypeScript）
 */
import { LeaseArbiter } from '../src/arbiter.ts';
import { LamportClock } from '../src/logical-clock.ts';
import { renderCausalChain, renderTimeline } from '../src/replay.ts';

const ARRIVAL_MS = 1_760_000_000_000; // 两个请求落入的同一物理毫秒
let nowMs = ARRIVAL_MS;

const arbiter = new LeaseArbiter({
  now: () => nowMs,
  hooks: {
    // 模拟慢仲裁：临界区被撑开 5ms，第二个请求在此窗口内到达
    afterLockAcquired: () => new Promise((r) => setTimeout(r, 5)),
  },
});

const clockA = new LamportClock('node-a');
const clockB = new LamportClock('node-b');

console.log(`[t=${nowMs}ms] node-a 与 node-b 同时申请主租约 "master"...\n`);

const [resultA, resultB] = await Promise.all([
  arbiter.acquire('node-a', 'master', 30_000, clockA.tick()),
  arbiter.acquire('node-b', 'master', 30_000, clockB.tick()),
]);

console.log('—— 仲裁结果 ——');
console.log(`node-a: ${resultA.ok ? `胜出，fencingToken=${resultA.handle.fencingToken}` : '失败'}`);
console.log(`node-b: ${resultB.ok ? '胜出' : `被拒，reason=${resultB.reason}`}\n`);

console.log(renderTimeline(arbiter.trail, 'master'));
console.log();
if (!resultB.ok) {
  console.log(renderCausalChain(arbiter.trail, resultB.rejectEvent.seq));
}
console.log();
console.log(`因果引用完整性校验: ${arbiter.trail.verifyCausalIntegrity().length === 0 ? '通过' : '失败'}`);
