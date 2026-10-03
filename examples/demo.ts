/**
 * 运行样例：npm run example
 *
 * 1) flatten：对复杂嵌套订单 JSON 自动推断全部叶子路径，建立虚拟列；
 * 2) project：仅指定 'user.address.city' 等少数路径时，只物化相关列，
 *    商品列表等大子树被整体跳过（不反序列化）。
 */
import { flatten, project } from '../src/index';
import { buildOrder } from '../test/fixture';

declare const console: { log(...args: unknown[]): void };

// 构造一笔含 500 个商品行的大订单，放大“跳过无关结构”的效果。
const order = buildOrder() as { items: unknown[]; [k: string]: unknown };
const [firstItem] = order.items;
for (let i = 0; i < 500; i++) {
  order.items.push({ ...(firstItem as object), sku: `SKU-BULK-${i}` });
}
const json = JSON.stringify(order, null, 2);

console.log('=== 输入: 复杂嵌套业务订单 JSON ===');
console.log(`JSON 文本大小: ${json.length} 字符, 商品行数: ${order.items.length}\n`);

// --- 1. 全量扁平化：自动推断字段路径 ---
const flat = flatten(json);
console.log('=== flatten: 自动推断的叶子路径（前 12 条） ===');
for (const p of flat.store.paths().slice(0, 12)) console.log(`  ${p}`);
console.log(`  ... 共 ${flat.store.columnCount} 个虚拟列\n`);

// --- 2. 按需投影：只取城市维度 ---
const { store, stats } = project(json, ['orderId', 'user.address.city']);
console.log("=== project: 仅投影 'orderId' 与 'user.address.city' ===");
console.log('物化的虚拟列:', store.paths());
console.log('orderId           ->', store.getColumn('orderId'));
console.log('user.address.city ->', store.getColumn('user.address.city'));
console.log('\n=== 投影统计（证明未反序列化无关结构） ===');
console.log(`  总字节数        : ${stats.totalBytes}`);
console.log(`  跳过字节数      : ${stats.skippedBytes} (${((stats.skippedBytes / stats.totalBytes) * 100).toFixed(1)}%)`);
console.log(`  跳过子树数      : ${stats.skippedSubtrees}`);
console.log(`  物化叶子值个数  : ${stats.materializedValues}`);
console.log('  商品列表 items 等无关结构被整体跳过，未产生任何对应列。');
