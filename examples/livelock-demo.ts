/**
 * 运行样例：用调试器定位一个因"等待未初始化资源"而假死的协程。
 * 运行：node examples/livelock-demo.ts
 */
import {
  Scheduler,
  Debugger,
  nop,
  sleep,
  requireResource,
  provideResource,
} from '../src/index.ts';

const scheduler = new Scheduler();
const dbg = new Debugger(scheduler);

// 业务协程：启动前必须拿到 db-config
scheduler.spawn('db-client', function* () {
  yield requireResource('db-config');
  yield nop();
});

// 有缺陷的初始化器：把资源名写错了（db-conf），db-config 永远无人初始化
scheduler.spawn('initializer', function* () {
  yield sleep(2);
  yield provideResource('db-conf');
});

scheduler.spawn('heartbeat', function* () {
  yield nop();
  yield nop();
  yield nop();
});

// 进入单步调试模式：全局时钟暂停，只能由调试器手动推动
dbg.enterStepMode();
try {
  scheduler.advanceTick();
} catch (err) {
  console.log(`[时钟已暂停] ${(err as Error).message}\n`);
}

console.log('=== 逐 Tick 手动推动调度器 ===');
for (let i = 0; i < 5; i++) {
  dbg.tick();
  console.log(dbg.snapshot());
  const events = dbg.transitionsAt(dbg.currentTick);
  for (const e of events) {
    console.log(`    └─ ${e.fiberName}: ${e.from} → ${e.to} (${e.note})`);
  }
  console.log();
}

console.log('=== 挂起原因字典 ===');
for (const [name, reason] of Object.entries(dbg.suspendReasons())) {
  console.log(`  ${name}: ${reason}`);
}

console.log('\n=== 假死检测报告 ===');
for (const stall of dbg.detectStall(2)) {
  console.log(
    `  ⚠ ${stall.fiberName} 自 tick=${stall.sinceTick} 起停滞 ${stall.stalledFor} 个 tick` +
    `（${stall.state}）：${stall.reason}`,
  );
}

console.log('\n=== db-client 的状态转换字典 ===');
for (const t of dbg.transitionsOf('db-client')) {
  console.log(`  tick=${t.tick} pc=${t.pc} ${t.from} → ${t.to} | ${t.note}`);
}

console.log('\n=== 重放 tick=3 时刻的系统状态 ===');
for (const [name, s] of Object.entries(dbg.stateAt(3))) {
  console.log(`  ${name}: ${s.state} (pc=${s.pc})`);
}

console.log('\n=== 定位后修复：补齐资源初始化 ===');
scheduler.spawn('fixer', function* () {
  yield provideResource('db-config', { url: 'postgres://localhost' });
});
dbg.tick(3);
console.log(dbg.snapshot());
console.log(`\ndb-client 最终状态: ${scheduler.getFiber('db-client')!.state}`);
