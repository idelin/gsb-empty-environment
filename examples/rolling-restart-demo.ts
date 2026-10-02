/**
 * 运行样例：主节点 A 收到滚动重启指令后的平滑优雅移交。
 *
 * 场景：
 *   1. 节点 A 持有 "orders" 资源租约，业务写入持续进行；
 *   2. A 收到滚动重启指令 → 冻结本地写入 → 向协调中心申请移交并指定候选 B；
 *   3. 交接包递交 B，B 立即继承剩余租约上下文并无缝接管主角色；
 *   4. 移交窗口内的业务写入被短暂排队，切换完成后补发，全程零错误。
 */
import { performance } from 'node:perf_hooks';
import { LeaseCoordinator, LeaseNode, LeaseError } from '../src/index.ts';

const RESOURCE = 'orders';
const coordinator = new LeaseCoordinator();
const nodeA = new LeaseNode('A', coordinator);
const nodeB = new LeaseNode('B', coordinator);

let writeErrors = 0;
const pendingQueue: string[] = [];
const committed: Array<{ node: string; value: string }> = [];

/** 业务写入入口：路由到当前主节点，冻结窗口内排队等待 */
function businessWrite(value: string): void {
  const primary = nodeA.isPrimary ? nodeA : nodeB.isPrimary ? nodeB : null;
  if (!primary || primary.isWritesFrozen) {
    pendingQueue.push(value);
    return;
  }
  try {
    primary.write(RESOURCE, value);
    committed.push({ node: primary.id, value });
  } catch (err) {
    if (err instanceof LeaseError && err.code === 'writes-frozen') {
      pendingQueue.push(value);
    } else {
      writeErrors += 1;
      console.error(`  [ERROR] 写入失败: ${(err as Error).message}`);
    }
  }
}

console.log('=== 租约控制器：滚动重启下的平滑优雅移交 ===\n');

// 1. A 获取租约成为主节点，业务写入开始
nodeA.acquire(RESOURCE, 5_000);
console.log(`[t0] 节点 A 获取租约成为主节点 (leaseId=${nodeA.lease!.leaseId.slice(0, 8)}…, epoch=1, TTL=5000ms)`);
businessWrite('order#1');
businessWrite('order#2');
console.log(`[t0] 业务写入正常: ${committed.length} 条已提交到 A`);

// 2. 滚动重启指令到达，A 冻结写入并发起移交
console.log('\n[指令] 运维下发滚动重启指令 → 节点 A 计划停机维护');
const switchStart = performance.now();
const pkg = nodeA.initiateHandover('B');
console.log(`[A] 已冻结本地写入，向协调中心申请移交，指定候选人 B`);
console.log(`[A] 交接包签发: 剩余TTL=${pkg.remainingTtlMs}ms, 新epoch=${pkg.epoch}, 到期时间原样保留`);

// 移交窗口内到达的写入被排队（不报错）
businessWrite('order#3');
console.log(`[业务] 移交窗口内 1 条写入进入暂存队列（队列长度=${pendingQueue.length}）`);

// 3. B 接收交接包，无缝接管
const inherited = nodeB.receiveHandover(pkg);
const switchoverMs = performance.now() - switchStart;
console.log(`[B] 接收交接包并立即接管主角色 (epoch=${inherited.epoch}, generation=${inherited.generation})`);
console.log(`[B] 继承剩余租约上下文: 到期时间不变，剩余 ${inherited.expiresAt - Date.now()}ms`);

// 4. 切换完成，补发排队写入
while (pendingQueue.length > 0) businessWrite(pendingQueue.shift()!);
businessWrite('order#4');

console.log('\n=== 验收结果 ===');
console.log(`  业务切换耗时        : ${switchoverMs.toFixed(3)} ms`);
console.log(`  写入错误数          : ${writeErrors}`);
console.log(`  已提交写入          : ${committed.map((c) => `${c.value}→${c.node}`).join(', ')}`);
console.log(`  A 状态              : isPrimary=${nodeA.isPrimary}（已卸任，可安全重启）`);
console.log(`  B 状态              : isPrimary=${nodeB.isPrimary}, 动态续期循环运行中`);

if (writeErrors !== 0) {
  console.error('\n验收失败：出现写入错误');
  process.exit(1);
}
if (switchoverMs >= 20) {
  console.error('\n验收失败：切换耗时超过毫秒级阈值');
  process.exit(1);
}
console.log('\n✔ 零错误中断，毫秒级平滑移交完成，A 可安全进入滚动重启');

nodeA.dispose();
nodeB.dispose();
