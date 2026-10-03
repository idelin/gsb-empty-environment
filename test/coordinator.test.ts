import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { EventLoop } from '../src/eventLoop.ts';
import { Bus } from '../src/bus.ts';
import { Wal } from '../src/wal.ts';
import { Coordinator, type CoordinatorOptions } from '../src/coordinator.ts';
import { Participant, type ParticipantOptions } from '../src/participant.ts';

interface Env {
  dir: string;
  loop: EventLoop;
  bus: Bus;
  walPath: (name: string) => string;
  mkParticipant: (id: string, opts?: ParticipantOptions) => Participant;
  mkCoordinator: (opts?: CoordinatorOptions) => Coordinator;
}

function makeEnv(): Env {
  const dir = mkdtempSync(join(tmpdir(), '2pc-test-'));
  const loop = new EventLoop();
  const bus = new Bus(loop);
  return {
    dir,
    loop,
    bus,
    walPath: (name) => join(dir, `${name}.wal`),
    mkParticipant: (id, opts) => new Participant(id, bus, loop, new Wal(join(dir, `${id}.wal`)), opts),
    mkCoordinator: (opts) => new Coordinator('coordinator', bus, loop, new Wal(join(dir, 'coordinator.wal')), opts),
  };
}

test('正常路径：全票 YES 后提交，所有参与者 COMMITTED，WAL 记录完整', () => {
  const env = makeEnv();
  const p1 = env.mkParticipant('p1');
  const p2 = env.mkParticipant('p2');
  const c = env.mkCoordinator();
  c.begin('tx1', ['p1', 'p2']);
  env.loop.run();

  assert.equal(p1.stateOf('tx1'), 'COMMITTED');
  assert.equal(p2.stateOf('tx1'), 'COMMITTED');
  assert.equal(c.stateOf('tx1'), 'DONE');
  const types = new Wal(env.walPath('coordinator')).replay().map((r) => r.type);
  assert.deepEqual(types, ['begin', 'decision', 'ack', 'ack', 'end']);
});

test('一票 NO：协调器中止事务，所有参与者 ABORTED', () => {
  const env = makeEnv();
  const p1 = env.mkParticipant('p1');
  const p2 = env.mkParticipant('p2', { vote: () => 'NO' });
  const c = env.mkCoordinator();
  c.begin('tx1', ['p1', 'p2']);
  env.loop.run();

  assert.equal(p1.stateOf('tx1'), 'ABORTED');
  assert.equal(p2.stateOf('tx1'), 'ABORTED');
  const decision = new Wal(env.walPath('coordinator')).replay().find((r) => r.type === 'decision');
  assert.equal(decision.decision, 'ABORT');
});

test('投票超时：参与者宕机未投票，协调器超时后中止', () => {
  const env = makeEnv();
  const p1 = env.mkParticipant('p1');
  const p2 = env.mkParticipant('p2');
  p2.crash(); // 投票前宕机
  const c = env.mkCoordinator({ prepareTimeout: 10 });
  c.begin('tx1', ['p1', 'p2']);
  env.loop.run();

  assert.equal(p1.stateOf('tx1'), 'ABORTED');
  const recs = new Wal(env.walPath('coordinator')).replay();
  assert.equal(recs.find((r) => r.type === 'decision').decision, 'ABORT');
  // p2 永久无响应，最终记入未决审计
  assert.ok(recs.some((r) => r.type === 'audit' && r.event === 'PENDING_PARTICIPANTS'));
});

test('重复投票与重复 Prepare 被去重，决议只产生一次', () => {
  const env = makeEnv();
  const p1 = env.mkParticipant('p1');
  const p2 = env.mkParticipant('p2');
  const c = env.mkCoordinator();
  c.begin('tx1', ['p1', 'p2']);
  // 重复投票（网络重传）
  env.bus.send('p1', 'coordinator', { type: 'vote', txId: 'tx1', vote: 'YES' });
  env.bus.send('p1', 'coordinator', { type: 'vote', txId: 'tx1', vote: 'YES' });
  // 重复 Prepare
  env.bus.send('coordinator', 'p1', { type: 'prepare', txId: 'tx1' });
  env.loop.run();

  assert.equal(p1.stateOf('tx1'), 'COMMITTED');
  assert.equal(p2.stateOf('tx1'), 'COMMITTED');
  const recs = new Wal(env.walPath('coordinator')).replay();
  assert.equal(recs.filter((r) => r.type === 'decision').length, 1);
  assert.equal(recs.filter((r) => r.type === 'end').length, 1);
});

test('验收1：协调器发布 Commit 前崩溃，重启后重放 WAL、查询参与者并完成提交', () => {
  const env = makeEnv();
  const p1 = env.mkParticipant('p1');
  const p2 = env.mkParticipant('p2');
  const p3 = env.mkParticipant('p3');
  const walFile = env.walPath('coordinator');

  let c1: Coordinator;
  c1 = new Coordinator('coordinator', env.bus, env.loop, new Wal(walFile), {
    hooks: { afterDecision: () => c1.crash() }, // 决议落盘后、发布前崩溃
  });
  c1.begin('tx1', ['p1', 'p2', 'p3']);
  env.loop.run();

  assert.equal(p1.stateOf('tx1'), 'PREPARED'); // Commit 未发出
  assert.equal(p2.stateOf('tx1'), 'PREPARED');
  assert.equal(p3.stateOf('tx1'), 'PREPARED');

  // 重启：新实例重放同一 WAL，向在线参与者查询并完成提交
  const c2 = new Coordinator('coordinator', env.bus, env.loop, new Wal(walFile));
  env.loop.run();

  assert.equal(p1.stateOf('tx1'), 'COMMITTED');
  assert.equal(p2.stateOf('tx1'), 'COMMITTED');
  assert.equal(p3.stateOf('tx1'), 'COMMITTED');
  assert.equal(c2.stateOf('tx1'), 'DONE');
  const recs = new Wal(walFile).replay();
  assert.equal(recs.filter((r) => r.type === 'decision' && r.decision === 'COMMIT').length, 1);
  assert.equal(recs.filter((r) => r.type === 'end').length, 1);
});

test('验收2：无响应参与者重试达上限后记录未决审计事件', () => {
  const env = makeEnv();
  const p1 = env.mkParticipant('p1');
  const p2 = env.mkParticipant('p2');
  const p3 = env.mkParticipant('p3');
  const walFile = env.walPath('coordinator');

  let c1: Coordinator;
  c1 = new Coordinator('coordinator', env.bus, env.loop, new Wal(walFile), {
    retryInterval: 5,
    maxRetries: 2,
    hooks: { afterDecision: () => { p3.crash(); c1.crash(); } },
  });
  c1.begin('tx1', ['p1', 'p2', 'p3']);
  env.loop.run();

  // 重启协调器，p3 保持离线
  const c2 = new Coordinator('coordinator', env.bus, env.loop, new Wal(walFile), {
    retryInterval: 5,
    maxRetries: 2,
  });
  env.loop.run();

  assert.equal(p1.stateOf('tx1'), 'COMMITTED');
  assert.equal(p2.stateOf('tx1'), 'COMMITTED');
  assert.equal(c2.stateOf('tx1'), 'DONE');
  const recs = new Wal(walFile).replay();
  const audits = recs.filter((r) => r.type === 'audit');
  assert.equal(audits.length, 1);
  assert.equal(audits[0].event, 'PENDING_PARTICIPANTS');
  assert.equal(audits[0].decision, 'COMMIT');
  assert.deepEqual(audits[0].participants, ['p3']);
  assert.equal(recs.filter((r) => r.type === 'end').length, 0); // 未全员确认，不写 end
});

test('参与者崩溃恢复后主动向协调器追问结果，不留孤儿事务', () => {
  const env = makeEnv();
  const p1 = env.mkParticipant('p1');
  const p2 = env.mkParticipant('p2');
  const p2Wal = env.walPath('p2');
  const c = env.mkCoordinator({ retryInterval: 10, maxRetries: 3 });

  c.begin('tx1', ['p1', 'p2']);
  // p2 投完 YES 后、收到决议前崩溃
  env.loop.schedule(1.5, () => p2.crash());
  // p2 重启：重放本地 WAL 发现 PREPARED，主动询问协调器
  let p2b!: Participant;
  env.loop.schedule(4, () => {
    p2b = new Participant('p2', env.bus, env.loop, new Wal(p2Wal));
  });
  env.loop.run();

  assert.equal(p1.stateOf('tx1'), 'COMMITTED');
  assert.equal(p2b.stateOf('tx1'), 'COMMITTED');
  assert.equal(c.stateOf('tx1'), 'DONE');
  const recs = new Wal(env.walPath('coordinator')).replay();
  assert.equal(recs.filter((r) => r.type === 'audit').length, 0);
  assert.equal(recs.filter((r) => r.type === 'end').length, 1);
});

test('协调器在 PREPARING 阶段崩溃：重启后推定中止，参与者释放资源', () => {
  const env = makeEnv();
  const p1 = env.mkParticipant('p1');
  const p2 = env.mkParticipant('p2');
  const walFile = env.walPath('coordinator');

  const c1 = new Coordinator('coordinator', env.bus, env.loop, new Wal(walFile));
  c1.begin('tx1', ['p1', 'p2']);
  env.loop.schedule(1.5, () => c1.crash()); // Prepare 已送达、投票未收齐时崩溃
  env.loop.run();
  assert.equal(p1.stateOf('tx1'), 'PREPARED');

  const c2 = new Coordinator('coordinator', env.bus, env.loop, new Wal(walFile));
  env.loop.run();

  assert.equal(p1.stateOf('tx1'), 'ABORTED');
  assert.equal(p2.stateOf('tx1'), 'ABORTED');
  const recs = new Wal(walFile).replay();
  assert.equal(recs.find((r) => r.type === 'decision').decision, 'ABORT');
  assert.equal(recs.filter((r) => r.type === 'end').length, 1);
});
