/**
 * 验收场景演示：
 * 1. 将系统置于单步调试模式；
 * 2. 逐个 Tick 手动推动调度器；
 * 3. 检查每个协程的状态迁移字典；
 * 4. 定位某协程在特定时隙因等待未初始化资源而假死。
 */
import { Debugger, Scheduler, provideResource, sleep, waitResource } from '../src/index.ts';

const scheduler = new Scheduler();
const dbg = new Debugger(scheduler); // 默认即单步调试模式：一切推进都需显式触发

// worker：依赖资源 "db-config"，初始化前只能干等
scheduler.spawn(function* () {
  yield sleep(1);
  const config = yield waitResource('db-config');
  yield sleep(1);
  console.log(`  [worker] 拿到配置，继续工作: ${JSON.stringify(config)}`);
}, 'worker');

// initializer：本应在 tick 2 前提供 "db-config"，却因笔误提供了 "db_config"
scheduler.spawn(function* () {
  yield sleep(1);
  yield provideResource('db_config', { host: 'localhost' }); // BUG: 资源名笔误
}, 'initializer');

console.log('== 单步调试模式：逐个 Tick 手动推动调度器 ==');
for (let i = 0; i < 6; i += 1) {
  dbg.tick();
  console.log(`-- tick=${scheduler.clock}`);
  for (const s of dbg.snapshot()) {
    console.log(`   ${s.id.padEnd(12)} pc=${s.pc} state=${s.state} ${s.suspendReason ?? ''}`);
  }
}

console.log('\n== 状态迁移字典 ==');
for (const [fiberId, history] of dbg.transitions()) {
  console.log(`  ${fiberId}:`);
  for (const t of history) {
    console.log(`    tick=${t.tick} ${t.from} -> ${t.to} (${t.reason})`);
  }
}

console.log('\n== 假死定位 ==');
const stuck = dbg.findStuckFibers(2, scheduler.clock);
for (const s of stuck) {
  console.log(
    `  协程 "${s.fiberId}" 自 tick=${s.stuckSince} 起无状态迁移，挂起原因: ${s.suspendReason}`,
  );
}
if (stuck.length === 0) console.log('  未发现假死协程');
