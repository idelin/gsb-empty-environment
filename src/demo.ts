/**
 * 运行样例：
 *  场景一（验收点1）：协调器在发布 Commit 前崩溃，重启后重放 WAL、查询参与者并完成提交。
 *  场景二（验收点2）：参与者永久无响应，重试达上限后记录未决审计事件。
 */
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import assert from 'node:assert/strict';
import { EventLoop } from './eventLoop.ts';
import { Bus } from './bus.ts';
import { Wal } from './wal.ts';
import { Coordinator } from './coordinator.ts';
import { Participant } from './participant.ts';

function scenarioCommitAfterCrash(): void {
  console.log('=== 场景一：协调器发布 Commit 前崩溃，重启后完成提交 ===');
  const dir = mkdtempSync(join(tmpdir(), '2pc-demo1-'));
  const loop = new EventLoop();
  const bus = new Bus(loop);
  const p1 = new Participant('p1', bus, loop, new Wal(join(dir, 'p1.wal')));
  const p2 = new Participant('p2', bus, loop, new Wal(join(dir, 'p2.wal')));
  const p3 = new Participant('p3', bus, loop, new Wal(join(dir, 'p3.wal')));
  const walPath = join(dir, 'coordinator.wal');

  let c1: Coordinator;
  c1 = new Coordinator('coordinator', bus, loop, new Wal(walPath), {
    hooks: { afterDecision: () => c1.crash() }, // 决议落盘后立即崩溃，Commit 尚未发出
  });
  c1.begin('tx-1', ['p1', 'p2', 'p3']);
  loop.run();

  console.log(`[t=${loop.now}] 协调器已崩溃。参与者状态: p1=${p1.stateOf('tx-1')} p2=${p2.stateOf('tx-1')} p3=${p3.stateOf('tx-1')}`);
  assert.equal(p1.stateOf('tx-1'), 'PREPARED');

  console.log('--- 重启协调器，重放 WAL 恢复 ---');
  const c2 = new Coordinator('coordinator', bus, loop, new Wal(walPath));
  loop.run();

  for (const p of [p1, p2, p3]) assert.equal(p.stateOf('tx-1'), 'COMMITTED');
  assert.equal(c2.stateOf('tx-1'), 'DONE');
  console.log(`[t=${loop.now}] 恢复完成。参与者状态: p1=${p1.stateOf('tx-1')} p2=${p2.stateOf('tx-1')} p3=${p3.stateOf('tx-1')}`);
  console.log('协调器 WAL:');
  for (const r of new Wal(walPath).replay()) console.log('  ' + JSON.stringify(r));
  console.log('场景一通过：崩溃前已落盘的 COMMIT 决议在重启后完成发布，无孤儿事务。\n');
}

function scenarioPendingAudit(): void {
  console.log('=== 场景二：参与者永久无响应，重试上限后记录未决审计 ===');
  const dir = mkdtempSync(join(tmpdir(), '2pc-demo2-'));
  const loop = new EventLoop();
  const bus = new Bus(loop);
  const p1 = new Participant('p1', bus, loop, new Wal(join(dir, 'p1.wal')));
  const p2 = new Participant('p2', bus, loop, new Wal(join(dir, 'p2.wal')));
  const p3 = new Participant('p3', bus, loop, new Wal(join(dir, 'p3.wal')));
  const walPath = join(dir, 'coordinator.wal');

  let c1: Coordinator;
  c1 = new Coordinator('coordinator', bus, loop, new Wal(walPath), {
    retryInterval: 5,
    maxRetries: 2,
    hooks: { afterDecision: () => { p3.crash(); c1.crash(); } }, // p3 永久宕机，协调器同时崩溃
  });
  c1.begin('tx-2', ['p1', 'p2', 'p3']);
  loop.run();
  console.log(`[t=${loop.now}] 协调器与 p3 均已崩溃。p1=${p1.stateOf('tx-2')} p2=${p2.stateOf('tx-2')}`);

  console.log('--- 重启协调器（p3 保持离线） ---');
  const c2 = new Coordinator('coordinator', bus, loop, new Wal(walPath), { retryInterval: 5, maxRetries: 2 });
  loop.run();

  assert.equal(p1.stateOf('tx-2'), 'COMMITTED');
  assert.equal(p2.stateOf('tx-2'), 'COMMITTED');
  const audits = new Wal(walPath).replay().filter((r) => r.type === 'audit');
  assert.equal(audits.length, 1);
  assert.deepEqual(audits[0].participants, ['p3']);
  console.log(`[t=${loop.now}] p1=${p1.stateOf('tx-2')} p2=${p2.stateOf('tx-2')}，p3 重试 2 次仍无响应`);
  console.log('未决审计事件: ' + JSON.stringify(audits[0]));
  console.log('场景二通过：在线参与者完成提交，无响应参与者记入未决审计。\n');
}

scenarioCommitAfterCrash();
scenarioPendingAudit();
console.log('全部场景断言通过。');
