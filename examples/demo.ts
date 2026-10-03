/**
 * 跨机房租约联邦演示：
 *   dc1(主) + dc2(备) + witness(第三方法官)
 *   1. 正常主备
 *   2. 光纤挖断：dc1 与 witness/dc2 失联
 *   3. 法官确认 dc1 失联，等旧租约过期后向 dc2 颁发晋升租约
 *   4. dc1 恢复，发现凭证失效，自动隔离为只读备机
 */
import { Network } from '../src/network.ts';
import { Witness } from '../src/witness.ts';
import { LeaseController } from '../src/controller.ts';

const TTL_MS = 1000;
const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

const network = new Network();
const witness = new Witness({ network });
witness.attach('witness');
const dc1 = new LeaseController({ site: 'dc1', peer: 'dc2', network, ttlMs: TTL_MS });
const dc2 = new LeaseController({ site: 'dc2', peer: 'dc1', network, ttlMs: TTL_MS });
dc1.attach();
dc2.attach();

function banner(title: string): void {
  console.log(`\n=== ${title} ${'='.repeat(Math.max(2, 60 - title.length))}`);
}

function roles(): string {
  const fmt = (c: LeaseController): string =>
    `${c.site}=${c.role}${c.lease ? `(term=${c.lease.term}, 剩余${Math.max(0, c.lease.expiresAt - Date.now())}ms)` : ''}`;
  return `${fmt(dc1)}  ${fmt(dc2)}`;
}

async function tryWrite(c: LeaseController, key: string, value: string): Promise<void> {
  try {
    const term = await c.write(key, value);
    console.log(`  写入 ${c.site} ${key}=${value} -> 成功 (fencing term=${term})`);
  } catch (err) {
    console.log(`  写入 ${c.site} ${key}=${value} -> 拒绝: ${(err as Error).message}`);
  }
}

banner('1. 正常态：dc1 竞选为主，dc2 为备');
await dc1.tick();
await dc2.tick();
console.log(' ', roles());
await tryWrite(dc1, 'order:1', 'created');
await tryWrite(dc2, 'order:1', 'hijack'); // 备机写应被拒绝

banner('2. 光纤挖断：dc1 <-> witness、dc1 <-> dc2 双向失联');
network.partition('dc1', 'witness');
network.partition('dc1', 'dc2');
console.log('  链路 dc1<->witness:', network.isCut('dc1->witness') ? 'DOWN' : 'UP',
  ' dc1<->dc2:', network.isCut('dc1->dc2') ? 'DOWN' : 'UP');
console.log('  分区期间 dc1 自以为仍是主，尝试写入（无法通过法官 fencing 校验）:');
await tryWrite(dc1, 'order:2', 'split-brain-write');

banner('3. dc2 检测到 peer 失联，请求法官仲裁接管');
console.log('  法官独立探测 dc1 ...', (await network.call('dc2', 'witness', '/lease/status') as object)
  ? '不可达（probe 失败）' : '可达');
const t0 = Date.now();
await dc2.tick();
console.log(`  法官确认 dc1 失联并等待旧租约过期后发证，接管耗时 ${Date.now() - t0}ms`);
console.log(' ', roles());
await tryWrite(dc2, 'order:2', 'taken-over');

banner('4. dc1 恢复：网络修复，旧主机检测凭证失效');
network.healPartition('dc1', 'witness');
network.healPartition('dc1', 'dc2');
console.log('  恢复瞬间:', roles());
await dc1.tick(); // 续租被法官拒绝 -> 自动隔离
console.log('  续租被拒后:', roles());
await tryWrite(dc1, 'order:3', 'zombie-write'); // 应被拒绝

banner('5. 终态：dc2 为唯一主，dc1 只读隔离');
await dc2.tick();
console.log(' ', roles());
await tryWrite(dc2, 'order:3', 'committed');
console.log('  dc1.store =', Object.fromEntries(dc1.store));
console.log('  dc2.store =', Object.fromEntries(dc2.store));

dc1.close();
dc2.close();
console.log('\n演示结束：全程任意时刻至多一个机房可写，无双写。');
