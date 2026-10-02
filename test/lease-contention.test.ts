/**
 * 验收测试：复现两个节点在同一物理毫秒内同时争抢主租约的竞态，
 * 并验证审计追踪器完整还原到达次序、锁状态变迁、拒绝响应与因果证据链。
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LeaseArbiter, type ArbiterHooks } from '../src/arbiter.ts';
import { LamportClock } from '../src/logical-clock.ts';
import { renderCausalChain, renderTimeline } from '../src/replay.ts';
import type { AuditEvent } from '../src/events.ts';

const LEASE = 'master';
const ARRIVAL_MS = 1_760_000_000_000; // 固定的物理毫秒：两个请求都落在这里

interface RaceSetup {
  arbiter: LeaseArbiter;
  events: readonly AuditEvent[];
  resultA: Awaited<ReturnType<LeaseArbiter['acquire']>>;
  resultB: Awaited<ReturnType<LeaseArbiter['acquire']>>;
}

/**
 * 复现竞态：node-a 与 node-b 在同一物理毫秒发起申请。
 * 仲裁器临界区被人为撑开 5ms（模拟慢仲裁），
 * 期间第二个请求已到达并排队 —— 典型的毫秒级争抢窗口。
 */
async function reproduceRace(): Promise<RaceSetup> {
  let nowMs = ARRIVAL_MS;
  const hooks: ArbiterHooks = {
    // 第一个请求进入临界区后阻塞 5ms，放大竞态窗口
    afterLockAcquired: () => new Promise((r) => setTimeout(r, 5)),
  };
  const arbiter = new LeaseArbiter({ now: () => nowMs, hooks });
  const clockA = new LamportClock('node-a');
  const clockB = new LamportClock('node-b');

  // 两个请求同步发出：到达次序 = 调用次序，墙钟同为 ARRIVAL_MS
  const promiseA = arbiter.acquire('node-a', LEASE, 30_000, clockA.tick());
  const promiseB = arbiter.acquire('node-b', LEASE, 30_000, clockB.tick());
  const [resultA, resultB] = await Promise.all([promiseA, promiseB]);
  nowMs += 100;
  return { arbiter, events: arbiter.trail.all(), resultA, resultB };
}

test('竞态复现：同一毫秒双请求，先到者胜出、后来者被拒', async () => {
  const { events, resultA, resultB } = await reproduceRace();

  // 一胜一负
  assert.equal(resultA.ok, true, 'node-a 应获得租约');
  assert.equal(resultB.ok, false, 'node-b 应被拒绝');
  if (!resultA.ok || resultB.ok) throw new Error('unreachable');
  assert.equal(resultB.reason, 'LEASE_HELD');
  assert.equal(resultA.handle.fencingToken, 1);

  // 两份请求的到达次序与"同一毫秒"证据
  const requests = events.filter((e) => e.kind === 'LEASE_REQUESTED');
  assert.equal(requests.length, 2);
  assert.equal(requests[0].actorId, 'node-a', '仲裁器观察到 node-a 先到达');
  assert.equal(requests[1].actorId, 'node-b');
  assert.equal(requests[0].wallTimeMs, ARRIVAL_MS);
  assert.equal(requests[1].wallTimeMs, ARRIVAL_MS, '两个请求落在同一物理毫秒');
  assert.ok(requests[0].seq < requests[1].seq, '审计流水序号给出确定性到达次序');
  // 双方本地逻辑时钟相同（各为 1），物理时钟也无法区分 —— 只有仲裁器观察次序可仲裁
  assert.deepEqual(requests[0].details.requesterClock, { lamport: 1, nodeId: 'node-a' });
  assert.deepEqual(requests[1].details.requesterClock, { lamport: 1, nodeId: 'node-b' });
  assert.ok(requests[0].clock.lamport < requests[1].clock.lamport, '仲裁器逻辑时钟严格定序');
});

test('仲裁器锁状态变迁完整且互斥（无嵌套获取）', async () => {
  const { events } = await reproduceRace();

  const lockEvents = events.filter(
    (e) => e.kind === 'ARBITER_LOCK_ACQUIRED' || e.kind === 'ARBITER_LOCK_RELEASED',
  );
  assert.equal(lockEvents.length, 4, '两次临界区 = 两次获取 + 两次释放');
  assert.deepEqual(
    lockEvents.map((e) => e.kind),
    ['ARBITER_LOCK_ACQUIRED', 'ARBITER_LOCK_RELEASED', 'ARBITER_LOCK_ACQUIRED', 'ARBITER_LOCK_RELEASED'],
    '锁变迁必须严格交替（互斥证据）',
  );
  assert.deepEqual(lockEvents[0].details, { operation: 'acquire:master', from: 'IDLE', to: 'LOCKED' });
  assert.deepEqual(lockEvents[3].details, { operation: 'acquire:master', from: 'LOCKED', to: 'IDLE' });
});

test('竞态冲突事件与拒绝响应构成完整证据链', async () => {
  const { arbiter, events, resultB } = await reproduceRace();
  if (resultB.ok) throw new Error('unreachable');

  const contention = events.find((e) => e.kind === 'CONTENTION_DETECTED');
  assert.ok(contention, '必须记录竞态冲突事件');
  assert.deepEqual(
    {
      winnerNodeId: contention.details.winnerNodeId,
      loserNodeId: contention.details.loserNodeId,
      arbiterLockState: contention.details.arbiterLockState,
      sameMillisecond: contention.details.sameMillisecond,
    },
    {
      winnerNodeId: 'node-a',
      loserNodeId: 'node-b',
      arbiterLockState: 'LOCKED',
      sameMillisecond: true,
    },
  );

  // 拒绝响应：原因 + 当前持有者 + fencing token
  const reject = resultB.rejectEvent;
  assert.equal(reject.kind, 'LEASE_REJECTED');
  assert.equal(reject.details.reason, 'LEASE_HELD');
  assert.equal(reject.details.currentHolderId, 'node-a');
  assert.equal(reject.details.currentFencingToken, 1);
  assert.ok(reject.causedBy.includes(contention.seq), '拒绝必须因果关联到冲突事件');
  assert.ok(reject.causedBy.includes(contention.details.loserRequestSeq as number), '拒绝必须因果关联到落败请求');

  // 因果证据链：从拒绝事件回溯到落败请求（根因）
  const chain = arbiter.trail.causalChain(reject.seq);
  assert.equal(chain.at(-1)!.seq, reject.seq);
  const chainKinds = chain.map((e) => e.kind);
  assert.ok(chainKinds.includes('LEASE_REQUESTED'), '证据链包含落败请求');
  assert.ok(chainKinds.includes('CONTENTION_DETECTED'), '证据链包含冲突检测');
  assert.ok(chainKinds.indexOf('LEASE_REQUESTED') < chainKinds.indexOf('CONTENTION_DETECTED'));
  assert.ok(chainKinds.indexOf('CONTENTION_DETECTED') < chainKinds.indexOf('LEASE_REJECTED'));

  // 全流水因果引用完整性
  assert.deepEqual(arbiter.trail.verifyCausalIntegrity(), []);
});

test('时间轴回放：逻辑时钟全序与记录次序一致', async () => {
  const { arbiter } = await reproduceRace();
  const timeline = arbiter.trail.timeline(LEASE);
  assert.deepEqual(
    timeline.map((e) => e.kind),
    [
      'LEASE_REQUESTED',
      'LEASE_REQUESTED',
      'ARBITER_LOCK_ACQUIRED',
      'LEASE_GRANTED',
      'ARBITER_LOCK_RELEASED',
      'ARBITER_LOCK_ACQUIRED',
      'CONTENTION_DETECTED',
      'LEASE_REJECTED',
      'ARBITER_LOCK_RELEASED',
    ],
  );
  // 逻辑时间戳严格递增
  for (let i = 1; i < timeline.length; i++) {
    assert.ok(timeline[i].clock.lamport > timeline[i - 1].clock.lamport);
  }
  // 回放渲染包含关键证据
  const text = renderTimeline(arbiter.trail, LEASE);
  assert.match(text, /node-a 申请租约/);
  assert.match(text, /检测到竞态冲突/);
  assert.match(text, /node-b 被拒绝（原因=LEASE_HELD/);
  const chainText = renderCausalChain(arbiter.trail, timeline.at(-2)!.seq);
  assert.match(chainText, /因果证据链/);
});

test('完整生命周期：申请→续期→移交→超时 全部落账', async () => {
  let nowMs = ARRIVAL_MS;
  const arbiter = new LeaseArbiter({ now: () => nowMs });

  const acquired = await arbiter.acquire('node-a', LEASE, 1_000);
  assert.equal(acquired.ok, true);
  if (!acquired.ok) throw new Error('unreachable');

  nowMs += 500;
  const renewed = await arbiter.renew('node-a', LEASE, acquired.handle.fencingToken, 1_000);
  assert.deepEqual(renewed, { ok: true });

  const transferred = await arbiter.transfer('node-a', 'node-b', LEASE, acquired.handle.fencingToken, 1_000);
  assert.deepEqual(transferred, { ok: true });
  assert.equal(arbiter.currentHolder()?.holderId, 'node-b');

  nowMs += 2_000;
  await arbiter.expire(LEASE);
  assert.equal(arbiter.currentHolder(), null);

  const kinds = arbiter.trail.all().map((e) => e.kind);
  for (const expected of ['LEASE_REQUESTED', 'LEASE_GRANTED', 'LEASE_RENEWED', 'LEASE_TRANSFERRED', 'LEASE_EXPIRED']) {
    assert.ok(kinds.includes(expected as never), `缺少事件 ${expected}`);
  }
  // fencing token 移交后递增
  const transferredEvent = arbiter.trail.all().find((e) => e.kind === 'LEASE_TRANSFERRED');
  assert.equal(transferredEvent?.details.fencingToken, 2);
  assert.deepEqual(arbiter.trail.verifyCausalIntegrity(), []);
});

test('防护性拒绝：非持有者续期与过期 fencing token', async () => {
  let nowMs = ARRIVAL_MS;
  const arbiter = new LeaseArbiter({ now: () => nowMs });
  const acquired = await arbiter.acquire('node-a', LEASE, 1_000);
  if (!acquired.ok) throw new Error('unreachable');

  const notHolder = await arbiter.renew('node-b', LEASE, acquired.handle.fencingToken, 1_000);
  assert.deepEqual(notHolder, { ok: false, reason: 'NOT_HOLDER' });

  const stale = await arbiter.renew('node-a', LEASE, 999, 1_000);
  assert.deepEqual(stale, { ok: false, reason: 'FENCING_TOKEN_STALE' });

  nowMs += 2_000;
  const expired = await arbiter.renew('node-a', LEASE, acquired.handle.fencingToken, 1_000);
  assert.deepEqual(expired, { ok: false, reason: 'LEASE_EXPIRED' });

  const rejects = arbiter.trail.all().filter((e) => e.kind === 'LEASE_REJECTED');
  assert.deepEqual(rejects.map((e) => e.details.reason), [
    'NOT_HOLDER',
    'FENCING_TOKEN_STALE',
    'LEASE_EXPIRED',
  ]);
});
