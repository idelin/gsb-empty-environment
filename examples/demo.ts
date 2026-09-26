/**
 * 运行样例：自建 RGBA 小图 -> 标注 -> 局部修改（合并 / 拆分）-> 观察稳定 ID。
 * 运行：node examples/demo.ts
 */
import { labelMapFromRGBA, makeRGBA, renderASCII } from "../src/rgba.ts";

// 9x5 小图：左侧 2x2 方块、右侧竖条、底部横条，共 3 个分量。
const width = 9;
const height = 5;
const data = makeRGBA(width, height, [
  // 分量 A：左上 2x2 方块
  [0, 0], [1, 0],
  [0, 1], [1, 1],
  // 分量 B：右侧竖条
  [8, 0], [8, 1], [8, 2],
  // 分量 C：底部横条
  [3, 4], [4, 4], [5, 4],
]);

const map = labelMapFromRGBA(data, width, height, 4);

function show(title: string): void {
  console.log(`\n== ${title} ==`);
  console.log(renderASCII(map));
  console.log(`分量数: ${map.componentCount}，存活 ID: [${map.ids().join(", ")}]`);
}

show("初始（3 个分量）");
const idA = map.labelAt(0, 0);
const idB = map.labelAt(8, 1);
const idC = map.labelAt(4, 4);
console.log(`A=${idA} B=${idB} C=${idC}`);

// 1) 合并：在 A 与 C 之间架桥 (1,2),(1,3),(2,4)... 简化为逐像素搭桥。
map.setPixel(1, 2, true);
map.setPixel(1, 3, true);
map.setPixel(1, 4, true);
map.setPixel(2, 4, true);
show("搭桥后：A 与 C 合并（保留最小 ID）");
console.log(`(0,0) 与 (4,4) 同分量: ${map.labelAt(0, 0) === map.labelAt(4, 4)}`);
console.log(`B 的 ID 未受影响: ${map.labelAt(8, 1) === idB}`);

// 2) 拆分：拆掉桥中间的 (1,3)。
map.setPixel(1, 3, false);
show("拆桥后：分裂为两块，最大块保留原 ID");
console.log(`(0,0) 与 (4,4) 已分开: ${map.labelAt(0, 0) !== map.labelAt(4, 4)}`);
console.log(`B 的 ID 仍然稳定: ${map.labelAt(8, 1) === idB}`);

// 3) 8 连通对照：同样的对角结构在 8 连通下会相连。
const diag = makeRGBA(2, 2, [[0, 0], [1, 1]]);
const m4 = labelMapFromRGBA(diag, 2, 2, 4);
const m8 = labelMapFromRGBA(diag, 2, 2, 8);
console.log("\n== 对角像素对照 ==");
console.log(`4 连通分量数: ${m4.componentCount}（分开）`);
console.log(`8 连通分量数: ${m8.componentCount}（相连）`);
