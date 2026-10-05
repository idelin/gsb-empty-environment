/**
 * 操作日志回放脚本：把 test/oplog.ts 中的操作日志在全新的 Store 上执行，
 * 打印完整事件流（通知/提交/检出/账本），输出可重定向保存、可逐行 diff。
 *
 * 用法：node scripts/replay.ts > replay-result.txt
 */
import { runOpLog } from "../test/harness.ts";
import { Store } from "../src/store.ts";
import { CAPACITY, INITIAL_STATE, OP_LOG } from "../test/oplog.ts";

console.log("# 操作日志回放");
console.log(`# 初始状态: ${JSON.stringify(INITIAL_STATE)}`);
console.log(`# 容量上限: ${CAPACITY} 个树节点`);
console.log("# --- 操作日志 ---");
for (const [index, op] of OP_LOG.entries()) {
  console.log(`op[${index}] ${JSON.stringify(op)}`);
}
console.log("# --- 回放事件流 ---");

const store = new Store(INITIAL_STATE, { capacity: CAPACITY });
const record: string[] = [];
runOpLog(store, OP_LOG, record);
for (const line of record) console.log(line);

console.log("# --- 终态 ---");
console.log(`head: v${store.version}`);
console.log(`state: ${JSON.stringify(store.state)}`);
console.log(`ledger: retainedNodes=${store.ledger.retainedNodes} versions=[${store.ledger.versions.join(",")}]`);
console.log(`listenerErrors: ${store.listenerErrors.length} (${store.listenerErrors.map((e) => (e as Error).message).join("; ")})`);
