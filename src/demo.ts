import { LeaseRouterGateway } from "./gateway.ts";
import { KeyRange } from "./key-range.ts";
import { StorageNode } from "./storage-node.ts";

/**
 * 运行样例：
 *  1. 初始分区 [A-Z) 租约授予 Node 1；
 *  2. 数据量超限，在 'M' 处分裂为 [A-M) / [M-Z)；
 *  3. 两个子区间分别授予 Node 1 / Node 2 独立租约；
 *  4. 'Apple' 与 'Zoo' 并发路由到两个节点，互不阻塞。
 */
async function main() {
  const gateway = new LeaseRouterGateway({ maxPartitionSize: 1000 });

  // Node 1 处理慢（100ms），Node 2 处理快（5ms），用于凸显并发隔离
  const node1 = new StorageNode("node-1", 100);
  const node2 = new StorageNode("node-2", 5);
  gateway.registerNode(node1);
  gateway.registerNode(node2);

  const root = gateway.bootstrap(new KeyRange("A", "Z"), "node-1");
  console.log(`初始分区 ${root.range}，租约持有者: ${root.lease!.holderId} (epoch=${root.lease!.epoch})`);

  // 数据过大，触发在 'M' 处分裂
  const result = gateway.reportSize(root.partitionId, 5000, "M")!;
  console.log(`数据量 5000 > 阈值 1000，分裂为:`);
  console.log(`  左子区间 ${result.left.range} -> ${result.leftLease.holderId} (epoch=${result.leftLease.epoch}, 继承自 parentEpoch=${result.leftLease.parentEpoch})`);
  console.log(`  右子区间 ${result.right.range} -> ${result.rightLease.holderId} (epoch=${result.rightLease.epoch}, 继承自 parentEpoch=${result.rightLease.parentEpoch})`);

  // 并发查询：'Apple' 落 [A-M)，'Zoo' 落 [M-Z)
  const started = Date.now();
  const [apple, zoo] = await Promise.all([
    gateway.handleRequest({ key: "Apple" }),
    gateway.handleRequest({ key: "Zoo" }),
  ]);
  const elapsed = Date.now() - started;

  console.log(`\n并发查询结果:`);
  console.log(`  'Apple' -> ${apple.nodeId} (processedAt=${apple.processedAt - started}ms)`);
  console.log(`  'Zoo'   -> ${zoo.nodeId} (processedAt=${zoo.processedAt - started}ms)`);
  console.log(`总耗时 ${elapsed}ms ≈ 慢节点耗时 100ms，而非串行的 100+5=105ms；`);
  console.log(`快的 Node 2 在 ${zoo.processedAt - started}ms 完成，未被慢的 Node 1 (${apple.processedAt - started}ms) 阻塞。`);
}

main();
