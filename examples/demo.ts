/**
 * 运行示例：语音 / 交互 / 批量 三类流量共链路。
 * 运行：node examples/demo.ts   （需要 Node >= 23.6，或 22.6+ 加 --experimental-strip-types）
 */
import { Scheduler } from '../src/scheduler.ts';

const sched = new Scheduler({
  voice: { tokenRate: 30, tokenCap: 60, maxQueueBytes: 500, weight: 30 },
  inter: { tokenRate: 20, tokenCap: 40, maxQueueBytes: 500, weight: 20 },
  bulk: { tokenRate: 50, tokenCap: 100, maxQueueBytes: 2500, weight: 10 },
});

// 语音：每 2 tick 一个 40B 小包；交互：每 5 tick 一个 120B 包
for (let t = 0; t < 20; t += 2) sched.submit({ id: `voice-${t}`, arrivalTick: t, bytes: 40, category: 'voice' });
for (let t = 0; t < 20; t += 5) sched.submit({ id: `inter-${t}`, arrivalTick: t, bytes: 120, category: 'inter' });
// 批量：两个 2000B 大包（第二个超队列上限被整包拒绝），外加一个重复提交演示幂等
sched.submit({ id: 'bulk-1', arrivalTick: 0, bytes: 2000, category: 'bulk' });
sched.submit({ id: 'bulk-2', arrivalTick: 0, bytes: 2000, category: 'bulk' });
sched.submit({ id: 'voice-0', arrivalTick: 1, bytes: 40, category: 'voice' }); // 重复，幂等
sched.submit({ id: 'voice-2', arrivalTick: 3, bytes: 41, category: 'voice' }); // 同 ID 不同内容，冲突

// tick 10 开始时原子缩容 bulk 队列；缩容后新包被拒，既有包照发
sched.reconfigure(10, { bulk: { tokenRate: 50, tokenCap: 100, maxQueueBytes: 500, weight: 10 } });
sched.submit({ id: 'bulk-3', arrivalTick: 12, bytes: 1000, category: 'bulk' });

const ticks = 24;
console.log('tick | budget sent | voice inter bulk | tokens(v/i/b) | queued(v/i/b) | events');
for (let t = 0; t < ticks; t++) {
  const l = sched.step(100);
  const sent = (c: string) => String(l.sentByCategory[c] ?? 0).padStart(5);
  const tok = (c: string) => String(l.tokensByCategory[c]).padStart(4);
  const qd = (c: string) => String(l.queuedBytesByCategory[c]).padStart(5);
  const ev = [
    ...l.completions.map((c) => `done:${c.id}`),
    ...l.drops.map((d) => `drop:${d.id}(${d.reason})`),
    ...l.duplicates.map((d) => `dup:${d.id}`),
  ].join(' ');
  console.log(
    `${String(l.tick).padStart(4)} | ${String(l.budget).padStart(6)} ${String(l.sentTotal).padStart(4)} |` +
    `${sent('voice')}${sent('inter')}${sent('bulk')} |` +
    ` ${tok('voice')}/${tok('inter')}/${tok('bulk')} |` +
    `${qd('voice')}/${qd('inter')}/${qd('bulk')} | ${ev}`,
  );
}

console.log('\ncompletions:');
for (const c of sched.completions) console.log(`  tick ${String(c.tick).padStart(2)}  ${c.id} (${c.category}, ${c.bytes}B)`);
console.log('drops:');
for (const d of sched.drops) console.log(`  tick ${String(d.tick).padStart(2)}  ${d.id} -> ${d.reason}`);
console.log('duplicates (idempotent, no re-enqueue):');
for (const d of sched.duplicates) console.log(`  tick ${String(d.tick).padStart(2)}  ${d.id}`);
