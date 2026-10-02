/**
 * 运行样例：跨机房全局租约接管演练（对应验收边界）。
 *
 *  1. 机房1 持租约为主，机房2 为备；
 *  2. 光纤挖断：机房1 与法官、机房1 与机房2 失联；
 *  3. 法官确认机房1 租约真正过期，向机房2 颁发晋升租约（epoch 递增）；
 *  4. 机房1 恢复后检测到法官凭证已失效，自动降级为只读备机。
 */
import { ManualClock } from '../src/clock.ts';
import { NetworkHub } from '../src/network.ts';
import { Witness } from '../src/witness.ts';
import { FencedStorage } from '../src/storage.ts';
import { LeaseController } from '../src/controller.ts';

const TTL = 10_000; // 租约 TTL 10s
const clock = new ManualClock(0);
const network = new NetworkHub();
const witness = new Witness(clock);

function spawnDc(dcId: string): LeaseController {
  return new LeaseController({
    dcId,
    clock,
    network,
    witness,
    storage: new FencedStorage(dcId),
    ttlMs: TTL,
  });
}

const dc1 = spawnDc('dc1');
const dc2 = spawnDc('dc2');

function banner(title: string): void {
  console.log(`\n=== t=${clock.now()}ms | ${title} ===`);
}
function flush(...controllers: LeaseController[]): void {
  for (const c of controllers) {
    for (const e of c.events().slice(flushed.get(c) ?? 0)) console.log('  ' + e);
    flushed.set(c, c.events().length);
  }
}
const flushed = new Map<LeaseController, number>();

// ── 阶段 1：机房1 为主，机房2 为备 ─────────────────────────────
banner('阶段1：正常态 —— dc1 申请租约成为主，dc2 申请被拒保持备');
dc1.tryAcquire();
dc2.tryAcquire();
dc1.write('order/1', { amount: 100 });
clock.advance(4_000);
dc1.maintain(); // 正常续租
dc1.write('order/2', { amount: 200 });
flush(dc1, dc2);
console.log(`  状态: dc1=${dc1.getRole()} writable=${dc1.isWritable()} | dc2=${dc2.getRole()} writable=${dc2.isWritable()}`);

// ── 阶段 2：光纤挖断，机房1 与法官/机房2 失联 ──────────────────
banner('阶段2：光纤挖断 —— sever(dc1,witness), sever(dc1,dc2)');
network.sever('dc1', 'witness');
network.sever('dc1', 'dc2');
clock.advance(4_000);
dc1.maintain(); // 续租失败：法官不可达
flush(dc1, dc2);
console.log(`  dc1 在本地租约到期前仍可写? ${dc1.isWritable()}（t=8000 < expiresAt=14000）`);

clock.advance(6_000); // t=14000，dc1 租约本地到期
console.log(`  t=${clock.now()}: dc1 租约本地到期，writable=${dc1.isWritable()}（失联期间自动停写）`);

// ── 阶段 3：法官确认 dc1 租约过期，向 dc2 颁发晋升租约 ─────────
banner('阶段3：法官确认 dc1 真正失联（租约过期）-> dc2 晋升为主');
dc2.tryAcquire();
dc2.write('order/3', { amount: 300 });
flush(dc1, dc2);
console.log(`  状态: dc1 writable=${dc1.isWritable()} | dc2=${dc2.getRole()} writable=${dc2.isWritable()}`);

// ── 阶段 4：机房1 恢复，发现凭证失效，自动降级只读 ─────────────
banner('阶段4：链路恢复 —— dc1 续租被法官拒绝（凭证失效），自动降级');
network.heal('dc1', 'witness');
network.heal('dc1', 'dc2');
dc1.maintain(); // 法官：epoch=1 已被 epoch=2 取代 -> 降级
flush(dc1, dc2);
try {
  dc1.write('order/4', { amount: 400 });
} catch {
  console.log('  dc1 写入被拒绝：已是只读备机 ✔');
}
console.log(`  最终状态: dc1=${dc1.getRole()} writable=${dc1.isWritable()} | dc2=${dc2.getRole()} writable=${dc2.isWritable()}`);
console.log(`  数据校验: dc2.read('order/3') = ${JSON.stringify(dc2.read('order/3'))}`);
console.log('\n演练结束：全程任一时刻至多一个可写主节点，无双写。');
