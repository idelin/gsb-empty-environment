/**
 * 验收场景演示：
 * 数据项 X 有版本 v1(t=1), v2(t=5), v3(t=10)；
 * 当前活跃最小 ReadView 为 t=8；GC 安全修剪 v1；
 * 所有只读查询依然能正确读到 v2 或 v3。
 */
import { MvccStore } from "../src/index.ts";

const store = new MvccStore<string>();

store.put("X", "v1", 1);
store.put("X", "v2", 5);
store.put("X", "v3", 10);
console.log("写入后版本链 (ts 新->旧):", store.chainTimestamps("X")); // [10, 5, 1]

// 只读事务开启，ReadView = t=8，成为全局最小活跃 ReadView。
const reader = store.beginRead(8);
console.log("全局最小活跃 ReadView:", store.minActiveReadTs); // 8

console.log("GC 前读 X @t=8 :", store.read("X", reader.readTs)); // v2
console.log("GC 前读 X @t=10:", store.read("X", 10)); // v3

const pruned = store.gc();
console.log(`GC 修剪了 ${pruned} 个废弃版本（v1 已释放）`);
console.log("GC 后版本链 (ts 新->旧):", store.chainTimestamps("X")); // [10, 5]

// 验证所有只读查询依然正确。
console.log("GC 后读 X @t=8 (活跃读者):", store.read("X", reader.readTs)); // v2
console.log("GC 后读 X @t=5          :", store.read("X", 5)); // v2
console.log("GC 后读 X @t=10         :", store.read("X", 10)); // v3
console.log("GC 后读 X @t=100        :", store.read("X", 100)); // v3

// 读者提交后水位线消失，再次 GC 可继续回收 v2。
store.endRead(reader);
console.log("读者结束后再次 GC 修剪:", store.gc()); // 1 (v2)
console.log("最终版本链 (ts 新->旧):", store.chainTimestamps("X")); // [10]
console.log("累计释放版本数:", store.prunedCount); // 2
