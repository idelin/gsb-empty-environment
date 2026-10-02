import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  Debugger,
  Scheduler,
  acquireLock,
  chanRecv,
  chanSend,
  provideResource,
  releaseLock,
  sleep,
  waitResource,
} from '../src/index.ts';

describe('断点', () => {
  it('在指定协程的指定指令处暂停，该指令暂不执行', () => {
    const scheduler = new Scheduler();
    const dbg = new Debugger(scheduler);
    scheduler.spawn(function* () {
      yield acquireLock('m1'); // pc=0
      yield acquireLock('m2'); // pc=1，断点
      yield acquireLock('m3'); // pc=2
    }, 'a');

    dbg.setBreakpoint('a', 1);
    dbg.tick(); // 执行 pc=0
    dbg.tick(); // pc=1 命中断点，指令不执行
    const fiber = scheduler.fibers[0];
    assert.equal(fiber.pc, 1, '命中断点时 pc 停在下一条待执行指令');
    const hits = dbg.drainBreakpointHits();
    assert.deepEqual(hits, [{ fiberId: 'a', pc: 1, tick: 2 }]);

    dbg.clearBreakpoint('a', 1);
    dbg.tick();
    assert.equal(fiber.pc, 2, '清除断点后继续执行');
  });
});

describe('全局时钟暂停', () => {
  it('暂停时钟时睡眠协程不到期，恢复后按原时刻唤醒', () => {
    const scheduler = new Scheduler();
    const dbg = new Debugger(scheduler);
    scheduler.spawn(function* () {
      yield sleep(2);
    }, 'sleeper');

    dbg.tick(); // tick=1，执行 sleep(2)，将在 tick=3 唤醒
    assert.equal(scheduler.fibers[0].state, 'sleeping');

    dbg.pauseClock();
    dbg.tick();
    dbg.tick();
    assert.equal(scheduler.clock, 1, '时钟暂停期间 tick 不推进');
    assert.equal(scheduler.fibers[0].state, 'sleeping', '时钟暂停时睡眠者不到期');

    dbg.resumeClock();
    dbg.tick(); // tick=2
    assert.equal(scheduler.fibers[0].state, 'sleeping');
    dbg.tick(); // tick=3，到期唤醒并完成
    assert.equal(scheduler.fibers[0].state, 'done');
  });
});

describe('单协程单指令步进', () => {
  it('stepFiber 只推进目标协程的一条指令，不影响其他协程与时钟', () => {
    const scheduler = new Scheduler();
    const dbg = new Debugger(scheduler);
    scheduler.spawn(function* () {
      yield sleep(5);
      yield sleep(5);
    }, 'x');
    scheduler.spawn(function* () {
      yield sleep(5);
    }, 'y');

    assert.equal(dbg.stepFiber('x'), true);
    assert.equal(scheduler.fibers[0].pc, 1);
    assert.equal(scheduler.fibers[1].pc, 0, '其他协程不受影响');
    assert.equal(scheduler.clock, 0, '时钟不推进');

    assert.equal(dbg.stepFiber('x'), false, '睡眠中的协程无法步进');
    assert.equal(dbg.stepFiber('y'), true);
    assert.equal(scheduler.fibers[1].pc, 1);
  });
});

describe('挂起原因查看', () => {
  it('区分 等待锁 / 等待Channel / 睡眠 / 等待资源', () => {
    const scheduler = new Scheduler();
    const dbg = new Debugger(scheduler);
    scheduler.spawn(function* () {
      yield acquireLock('mu');
      yield sleep(100);
    }, 'lock-holder');
    scheduler.spawn(function* () {
      yield acquireLock('mu');
    }, 'lock-waiter');
    scheduler.spawn(function* () {
      yield chanRecv('ch');
    }, 'chan-waiter');
    scheduler.spawn(function* () {
      yield waitResource('cfg');
    }, 'res-waiter');

    dbg.tick(); // lock-holder 获取锁；其余三者分别进入等待锁/Channel/资源
    dbg.tick(); // lock-holder 执行 sleep(100)
    const snap = Object.fromEntries(dbg.snapshot().map((s) => [s.id, s]));
    assert.equal(snap['lock-holder'].state, 'sleeping');
    assert.match(snap['lock-holder'].suspendReason!, /sleeping until tick 102/);
    assert.equal(snap['lock-waiter'].state, 'waiting-lock');
    assert.match(snap['lock-waiter'].suspendReason!, /waiting lock "mu"/);
    assert.equal(snap['chan-waiter'].state, 'waiting-channel');
    assert.match(snap['chan-waiter'].suspendReason!, /waiting channel "ch"/);
    assert.equal(snap['res-waiter'].state, 'waiting-resource');
    assert.match(snap['res-waiter'].suspendReason!, /waiting resource "cfg"/);
  });

  it('锁释放与 Channel 发送会唤醒对应等待者', () => {
    const scheduler = new Scheduler();
    const dbg = new Debugger(scheduler);
    scheduler.spawn(function* () {
      yield acquireLock('mu');
      yield releaseLock('mu');
    }, 'owner');
    scheduler.spawn(function* () {
      yield acquireLock('mu');
    }, 'waiter');
    scheduler.spawn(function* () {
      yield chanSend('ch', 42);
    }, 'sender');
    scheduler.spawn(function* () {
      const v = yield chanRecv('ch');
      assert.equal(v, 42);
    }, 'receiver');

    dbg.tick();
    dbg.tick();
    dbg.tick();
    const snap = Object.fromEntries(dbg.snapshot().map((s) => [s.id, s]));
    assert.equal(snap['owner'].state, 'done');
    assert.equal(snap['waiter'].state, 'done');
    assert.equal(snap['receiver'].state, 'done');
  });
});

describe('验收：假死定位', () => {
  it('单步模式下逐 Tick 推动，凭状态迁移字典定位等待未初始化资源的协程', () => {
    const scheduler = new Scheduler();
    const dbg = new Debugger(scheduler);

    scheduler.spawn(function* () {
      yield sleep(1);
      yield waitResource('db-config'); // 永远等不到：提供者写错了资源名
      yield sleep(1);
    }, 'worker');
    scheduler.spawn(function* () {
      yield sleep(1);
      yield provideResource('db_config', { host: 'localhost' });
    }, 'initializer');

    // 单步调试模式：逐个 Tick 手动推动
    for (let i = 0; i < 6; i += 1) dbg.tick();

    const workerHistory = dbg.transitions().get('worker')!;
    const waitEntry = workerHistory.find((t) => t.to === 'waiting-resource');
    assert.ok(waitEntry, 'worker 应进入 waiting-resource');
    assert.equal(waitEntry!.tick, 2, 'worker 在 tick=2 因等待资源挂起');
    assert.match(waitEntry!.reason, /not initialized/);
    assert.equal(
      workerHistory[workerHistory.length - 1],
      waitEntry,
      '挂起后再无状态迁移：假死',
    );

    const stuck = dbg.findStuckFibers(2, scheduler.clock);
    assert.deepEqual(
      stuck.map((s) => s.fiberId),
      ['worker'],
    );
    assert.equal(stuck[0].stuckSince, 2);
    assert.match(stuck[0].suspendReason, /"db-config".*not initialized/);

    // 修复后（提供正确资源名）协程可恢复
    scheduler.spawn(function* () {
      yield provideResource('db-config', { host: 'localhost' });
    }, 'fixer');
    dbg.tick();
    dbg.tick();
    dbg.tick();
    const snap = Object.fromEntries(dbg.snapshot().map((s) => [s.id, s]));
    assert.equal(snap['worker'].state, 'done');
  });
});
