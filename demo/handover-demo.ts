import { LeaseCoordinator } from '../src/coordinator.ts';
import { LeaseNode } from '../src/node.ts';

/**
 * 运行样例：主节点 A 收到滚动重启指令，冻结本地写入并向候选节点 B
 * 移交租约凭证；B 收到交接包后立即无缝接管主角色。
 */
const RESOURCE = 'payment-service-leader';

const coordinator = new LeaseCoordinator({ minTtlMs: 200, maxTtlMs: 10_000, defaultTtlMs: 2_000 });
const nodeA = new LeaseNode('A', coordinator, { ttlMs: 2_000 });
const nodeB = new LeaseNode('B', coordinator, { ttlMs: 2_000 });

let clientErrors = 0;
let clientWrites = 0;
function clientWrite(node: LeaseNode, data: string): void {
  try {
    node.write(data);
    clientWrites += 1;
  } catch {
    clientErrors += 1;
  }
}

console.log('=== 租约控制器：动态续期 + 平滑优雅移交 运行样例 ===\n');

// 1. A 竞选成为主节点
nodeA.acquire(RESOURCE);
console.log(`[t=0ms]  A 获取租约，成为主节点 (fencing=${nodeA.fencingToken})`);

// 2. 正常业务流量
for (let i = 0; i < 5; i += 1) clientWrite(nodeA, `order-${i}`);
console.log(`[t≈0ms]  A 处理 ${nodeA.appliedWrites.length} 笔写入，自动续期循环运行中 (间隔≈${nodeA.renewalIntervalMs().toFixed(0)}ms)`);

// 3. A 收到滚动重启指令：先冻结本地写入
nodeA.freeze();
clientWrite(nodeA, 'order-during-freeze'); // 冻结期写入进入待移交队列，不报错
console.log('[t≈0ms]  A 收到滚动重启指令，已冻结本地写入（冻结期 1 笔写入进入待移交队列）');

// 4. A 向 B 移交租约凭证，B 立即接管
const metrics = nodeA.handoverTo(nodeB);
console.log(`[t=+${metrics.switchoverMs.toFixed(3)}ms]  交接完成：B 无缝接管主角色 (fencing=${nodeB.fencingToken})`);

// 5. 后续流量打到 B
for (let i = 5; i < 10; i += 1) clientWrite(nodeB, `order-${i}`);

// 6. A 的旧凭证已失效（防脑裂演示）
const staleToken = {
  resourceId: RESOURCE,
  holderId: 'A',
  fencingToken: 1,
  expiresAt: Date.now() + 2_000,
  leaseVersion: 1,
};
let fencingRejected = false;
try {
  coordinator.validateWrite(staleToken);
} catch {
  fencingRejected = true;
}

console.log('\n--- 验收结果 ---');
console.log(`当前持有者           : ${coordinator.currentHolder(RESOURCE)}`);
console.log(`业务切换耗时         : ${metrics.switchoverMs.toFixed(3)} ms`);
console.log(`客户端写入总数       : ${clientWrites}`);
console.log(`客户端错误数         : ${clientErrors}（零错误中断）`);
console.log(`B 继承回放的冻结写入 : ${JSON.stringify(nodeB.appliedWrites.filter((w) => w.includes('freeze')))}`);
console.log(`A 旧凭证写入被拒绝   : ${fencingRejected}（栅栏令牌防脑裂）`);

nodeA.shutdown();
nodeB.shutdown();
