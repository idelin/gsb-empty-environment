import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Scheduler,
  Debugger,
  resetFiberIds,
  nop,
  sleep,
  lock,
  unlock,
  send,
  recv,
  requireResource,
  provideResource,
} from '../src/index.ts';

test('单步调试模式：暂停全局时钟，仅允许调试器逐 Tick 手动推动', () => {
  resetFiberIds();
  const scheduler = new Scheduler();
  const dbg = new Debugger(scheduler);

  scheduler.spawn('a', function* () {
    yield nop();
    yield nop();
  });

  dbg.enterStepMode();
  assert.equal(dbg.stepMode, true);
  assert.equal(scheduler.clockPaused, true);

  // 全局时钟已暂停：直接推动调度器会被拒绝
  assert.throws(() => scheduler.advanceTick(), /全局时钟已被调试器暂停/);
  assert.equal(scheduler.tick, 0);

  // 调试器逐个 Tick 手动推动
  dbg.tick();
  assert.equal(scheduler.tick, 1);
  dbg.tick();
  assert.equal(scheduler.tick, 2);

  // 退出单步模式后时钟恢复
  dbg.exitStepMode();
  scheduler.advanceTick();
  assert.equal(scheduler.tick, 3);
});

test('断点：命中后暂停协程，可单步执行该协程的一条指令并继续', () => {
  resetFiberIds();
  const scheduler = new Scheduler();
  const dbg = new Debugger(scheduler);

  scheduler.spawn('c', function* () {
    yield nop(); // pc=0
    yield nop(); // pc=1 ← 断点
    yield nop(); // pc=2
  });
  dbg.enterStepMode();
  dbg.setBreakpoint('c', 1);

  dbg.tick(); // tick=1：执行 pc=0
  assert.equal(scheduler.getFiber('c')!.pc, 1);

  dbg.tick(); // tick=2：pc=1 命中断点，不执行
  const c = scheduler.getFiber('c')!;
  assert.equal(c.state, 'paused-breakpoint');
  assert.equal(c.pc, 1);
  assert.deepEqual(dbg.suspendReasons()['c'], '断点暂停 @pc=1');

  // 单步执行一条指令：pc 前进 1，全局时钟不变
  const tickBefore = scheduler.tick;
  assert.equal(dbg.stepInstruction('c'), true);
  assert.equal(c.pc, 2);
  assert.equal(c.state, 'ready');
  assert.equal(scheduler.tick, tickBefore);

  dbg.tick(); // tick=3：执行 pc=2
  dbg.tick(); // tick=4：协程结束
  assert.equal(c.state, 'done');
});

test('挂起原因字典：睡眠 / 等待锁 / 等待Channel / 等待资源', () => {
  resetFiberIds();
  const scheduler = new Scheduler();
  const dbg = new Debugger(scheduler);

  scheduler.spawn('sleeper', function* () {
    yield sleep(10);
  });
  scheduler.spawn('lock-holder', function* () {
    yield lock('mu');
    yield sleep(10);
    yield unlock('mu');
  });
  scheduler.spawn('lock-waiter', function* () {
    yield lock('mu');
    yield unlock('mu');
  });
  scheduler.spawn('chan-waiter', function* () {
    yield recv('ch');
  });
  scheduler.spawn('res-waiter', function* () {
    yield requireResource('cfg');
  });

  dbg.enterStepMode();
  dbg.tick(); // tick=1：全部执行第一条指令
  dbg.tick(); // tick=2：lock-holder 进入睡眠，lock-waiter 仍等锁

  const reasons = dbg.suspendReasons();
  assert.equal(reasons['sleeper'], '睡眠至 tick=11');
  assert.equal(reasons['lock-holder'], '睡眠至 tick=12');
  assert.equal(reasons['lock-waiter'], '等待锁 "mu"');
  assert.equal(reasons['chan-waiter'], '等待 Channel "ch" 可读');
  assert.equal(reasons['res-waiter'], '等待资源 "cfg" 初始化');
});

test('会合 Channel 与互斥锁的移交顺序确定', () => {
  resetFiberIds();
  const scheduler = new Scheduler();
  const dbg = new Debugger(scheduler);
  const received: unknown[] = [];

  scheduler.spawn('producer', function* () {
    yield send('ch', 42); // 无接收者 → 阻塞
  });
  scheduler.spawn('consumer', function* () {
    const v = yield recv('ch');
    received.push(v);
  });

  dbg.enterStepMode();
  dbg.tick(); // tick=1：producer 阻塞在 send；consumer 与其会合
  assert.equal(scheduler.getFiber('producer')!.state, 'ready'); // 会合后被唤醒
  dbg.tick(); // tick=2
  dbg.tick(); // tick=3
  assert.deepEqual(received, [42]);
  assert.equal(scheduler.getFiber('producer')!.state, 'done');
  assert.equal(scheduler.getFiber('consumer')!.state, 'done');
});

test('验收：状态转换字典定位"等待未初始化资源"导致的假死', () => {
  resetFiberIds();
  const scheduler = new Scheduler();
  const dbg = new Debugger(scheduler);

  // db-client 依赖资源 db-config 才能继续
  scheduler.spawn('db-client', function* () {
    yield requireResource('db-config');
    yield nop();
  });
  // 有缺陷的初始化器：提供了错误名字的资源，db-config 永远不会被初始化
  scheduler.spawn('initializer', function* () {
    yield sleep(2);
    yield provideResource('db-conf'); // ← Bug：拼写错误
  });
  // 系统其余部分正常运转
  scheduler.spawn('heartbeat', function* () {
    yield nop();
    yield nop();
    yield nop();
  });

  dbg.enterStepMode();

  // 逐个 Tick 手动推动调度器
  dbg.tick(); // tick=1：db-client 挂起于 require("db-config")
  dbg.tick(); // tick=2
  dbg.tick(); // tick=3：initializer 醒来，提供了错误的资源名
  dbg.tick(); // tick=4：initializer、heartbeat 陆续完成
  dbg.tick(); // tick=5：系统静默，但 db-client 永远等不到资源 → 假死

  // 1) 挂起原因字典直接暴露问题
  assert.equal(dbg.suspendReasons()['db-client'], '等待资源 "db-config" 初始化');

  // 2) 假死检测：db-client 自 tick=1 起停滞 4 个 tick
  const stalls = dbg.detectStall(2);
  assert.equal(stalls.length, 1);
  assert.equal(stalls[0].fiberName, 'db-client');
  assert.equal(stalls[0].state, 'waiting-resource');
  assert.equal(stalls[0].sinceTick, 1);
  assert.equal(stalls[0].stalledFor, 4);
  assert.match(stalls[0].reason, /等待资源 "db-config" 初始化/);

  // 3) 状态转换字典：db-client 在 tick=1 进入等待后再无转换
  const clientTransitions = dbg.transitionsOf('db-client');
  assert.equal(clientTransitions.length, 1);
  assert.equal(clientTransitions[0].tick, 1);
  assert.equal(clientTransitions[0].to, 'waiting-resource');
  assert.match(clientTransitions[0].note, /资源未初始化/);

  // 4) 可重放任意时隙的系统状态
  assert.equal(dbg.stateAt(0)['db-client'].state, 'ready');
  assert.equal(dbg.stateAt(1)['db-client'].state, 'waiting-resource');
  assert.equal(dbg.stateAt(5)['initializer'].state, 'done');
  assert.equal(dbg.stateAt(5)['heartbeat'].state, 'done');
  assert.equal(dbg.stateAt(5)['db-client'].state, 'waiting-resource');

  // 5) 定位后修复：补齐资源初始化，假死解除
  scheduler.spawn('fixer', function* () {
    yield provideResource('db-config', { url: 'postgres://localhost' });
  });
  dbg.tick(); // tick=6：fixer 初始化资源，唤醒 db-client
  dbg.tick(); // tick=7：db-client 继续执行
  dbg.tick(); // tick=8：db-client 完成
  assert.equal(scheduler.getFiber('db-client')!.state, 'done');
  assert.equal(dbg.detectStall(1).length, 0);
});
