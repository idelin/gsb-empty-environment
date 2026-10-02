/**
 * 运行样例：验收边界场景演示。
 *
 * 数据项 X 有版本 v1(t=1), v2(t=5), v3(t=10)；
 * 当前活跃最小 ReadView 为 t=8；
 * GC 安全修剪 v1 并释放内存；只读查询仍能读到 v2 或 v3。
 *
 * 运行：npm run demo
 */
import { MvccStore, Version } from "../src/mvcc.ts";

const store = new MvccStore();

console.log("== 1. 写入 X 的三个版本 ==");
store.put("X", 1, "v1");
store.put("X", 5, "v2");
store.put("X", 10, "v3");
console.log(`   版本链(新->旧): t=[${store.chain("X")!.timestamps()}]`);
console.log(`   存活版本节点数: ${Version.allocated}`);

console.log("\n== 2. 开启两个只读事务：ReadView t=8 与 t=12 ==");
const rv8 = store.registry.begin(8);
const rv12 = store.registry.begin(12);
console.log(`   全局最小活跃 ReadView: t=${store.registry.minActive}`);

console.log("\n== 3. 执行 GC ==");
const report = store.gc();
console.log(`   修剪结果: X -> t=[${report.pruned.get("X")}]，释放 ${report.freedVersions} 个版本节点`);
console.log(`   修剪后版本链: t=[${store.chain("X")!.timestamps()}]`);
console.log(`   存活版本节点数: ${Version.allocated}（内存已释放）`);

console.log("\n== 4. 验证只读查询（可重复读） ==");
console.log(`   ReadView t=8  读 X -> ${store.read("X", rv8)}（期望 v2）`);
console.log(`   ReadView t=12 读 X -> ${store.read("X", rv12)}（期望 v3）`);
console.log(`   ReadView t=8  再读 -> ${store.read("X", rv8)}（可重复读，仍为 v2）`);
console.log(`   新读者 t=9  读 X -> ${store.read("X", 9)}（期望 v2）`);
console.log(`   新读者 t=10 读 X -> ${store.read("X", 10)}（期望 v3）`);

store.registry.end(rv8);
store.registry.end(rv12);
console.log("\n== 5. 读者全部结束后再次 GC：仅保留最新版本 ==");
const report2 = store.gc();
console.log(`   修剪结果: X -> t=[${report2.pruned.get("X")}]`);
console.log(`   最终版本链: t=[${store.chain("X")!.timestamps()}]，存活节点数: ${Version.allocated}`);
