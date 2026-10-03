/**
 * 运行样例：
 *  1) ARIES：构造含 3 个未提交 + 2 个已提交事务的 WAL 文件，
 *     Checkpoint 后崩溃，执行三阶段恢复并打印每阶段结果。
 *  2) 路由：构建 2DC×2Rack×3Node 拓扑，演示局部性感知路由与节点扩缩容。
 */
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { WriteAheadLog } from "../src/aries/wal.ts";
import { DiskStorage, BufferPool } from "../src/aries/storage.ts";
import { TransactionManager } from "../src/aries/transaction.ts";
import { RecoveryManager } from "../src/aries/recovery.ts";
import { buildUniformTopology } from "../src/shard/vnodes.ts";
import { LocalityAwareRouter } from "../src/shard/router.ts";

function ariesDemo(): void {
  console.log("=== ARIES 崩溃恢复演示 ===");
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "aries-demo-"));
  const walPath = path.join(dir, "wal.log");
  const wal = new WriteAheadLog(walPath);
  const disk = new DiskStorage();
  const pool = new BufferPool(disk);
  const tm = new TransactionManager(wal, pool);

  tm.begin(1);
  tm.update(1, 1, "A1");
  tm.update(1, 2, "B1");
  tm.commit(1); // T1 已提交

  tm.begin(3);
  tm.update(3, 1, "A3"); // T3 未提交
  pool.flushAll(); // 部分刷盘
  const ckpt = tm.checkpoint();
  console.log(`Checkpoint @ LSN=${ckpt}（T3 活跃）`);

  tm.begin(2);
  tm.update(2, 3, "C2");
  tm.commit(2); // T2 Checkpoint 后提交

  tm.begin(4);
  tm.update(4, 2, "B4"); // T4 未提交
  tm.begin(5);
  tm.update(5, 3, "C5"); // T5 未提交
  tm.update(5, 4, "D5");
  tm.update(3, 4, "D3");

  pool.crash();
  console.log(`崩溃！WAL 文件: ${walPath}（${wal.size} 条日志）\n`);

  const wal2 = WriteAheadLog.recoverFromFile(walPath);
  const rm = new RecoveryManager(wal2, disk);

  const analysis = rm.analysis();
  console.log("[Analysis]");
  console.log(`  失败事务: [${analysis.loserTxns.join(", ")}]`);
  console.log(`  DPT 页数: ${analysis.dirtyPageTable.size}, Redo 起点 LSN=${analysis.redoLSN}`);

  const redone = rm.redo(analysis);
  console.log(`[Redo] 重放 ${redone.length} 条日志: [${redone.join(", ")}]`);

  const { undoneLSNs, clrLSNs } = rm.undo(analysis);
  console.log(`[Undo] 撤销 ${undoneLSNs.length} 条: [${undoneLSNs.join(", ")}]`);
  console.log(`       写入 CLR: [${clrLSNs.join(", ")}]`);

  console.log("\n恢复后磁盘状态:");
  for (const p of [1, 2, 3, 4]) {
    const page = disk.readPage(p);
    console.log(`  P${p} = "${page.data}" (pageLSN=${page.pageLSN})`);
  }
  console.log("期望: P1=A1, P2=B1, P3=C2, P4=\"\"（已提交持久化，未提交回滚）\n");
}

async function routerDemo(): Promise<void> {
  console.log("=== 局部性感知路由演示 ===");
  const topo = buildUniformTopology(2, 2, 3); // 12 节点
  const router = new LocalityAwareRouter(topo, 512);
  const client = "dc0-rack0-node0";

  const dist = [0, 0, 0, 0];
  for (let i = 0; i < 10000; i++) {
    dist[router.route(`user-${i}`, client).distance]++;
  }
  console.log(`客户端 ${client} 的 10000 次路由距离分布:`);
  console.log(`  同节点=${dist[0]} 同机架=${dist[1]} 同DC=${dist[2]} 跨DC=${dist[3]}`);
  console.log(`  本地命中率: ${(((dist[0] + dist[1]) / 10000) * 100).toFixed(1)}%`);

  const add = await router.addNode({
    nodeId: "dc0-rack0-node3",
    rackId: "dc0-rack0",
    dcId: "dc0",
    capacity: 1,
  });
  console.log(`扩容 1 节点: 迁移 ${add.moved}/512 分片（最小迁移）`);
  const remove = await router.removeNode("dc0-rack0-node3");
  console.log(`缩容 1 节点: 迁移 ${remove.moved}/512 分片`);
  router.assertNoLeak();
  console.log("无泄漏自检通过");
}

ariesDemo();
await routerDemo();
