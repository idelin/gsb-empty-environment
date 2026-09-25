/**
 * 运行样例：可变行高列表的锚定滚动演示。
 * 运行：node examples/demo.ts （Node >= 22.6，无需编译）
 */
import { ListModel, type MeasureToken } from "../src/list-model.ts";
import { VirtualList } from "../src/scroller.ts";
import { makeRowFixture, measureNode } from "./fixtures.ts";

const VIEWPORT = 200;
const TEXT_WIDTH = 160;

const model = new ListModel(/* defaultEstimate */ 40);
const list = new VirtualList(model, VIEWPORT);

// 1) 初始填充：估计行高 40，含一个可折叠分组
for (let i = 0; i < 30; i++) model.append({ id: `row-${i}` });
model.append({ id: "group-A" });
for (let i = 0; i < 4; i++) model.append({ id: `gA-${i}`, parentId: "group-A" });
for (let i = 30; i < 60; i++) model.append({ id: `row-${i}` });

function show(tag: string): void {
  const anchor = list.getAnchor();
  const range = list.visibleRange();
  console.log(
    `${tag}\n  scrollTop=${list.scrollTop}  total=${model.totalHeight()}  version=${model.version}\n` +
      `  anchor=${anchor ? `${anchor.id}+${anchor.offset}` : "(none)"}  visible=[${range.ids.join(", ")}]`,
  );
}

// 2) 异步实测：签发令牌 → “布局任务”用夹具字形度量算高 → 入队稍后应用
const pending: Array<{ token: MeasureToken; height: number }> = [];
function requestAsyncMeasure(id: string): void {
  const token = model.requestMeasure(id);
  if (!token) return;
  const node = makeRowFixture(Number(id.replace(/\D/g, "")) || 0);
  const height = measureNode(node, TEXT_WIDTH); // 纯算术，无浏览器布局引擎
  pending.push({ token, height }); // 模拟异步：稍后才 flush
}
function flushMeasures(): number {
  let applied = 0;
  for (const { token, height } of pending.splice(0)) {
    if (list.applyMeasure(token, height)) applied++;
  }
  return applied;
}

list.jumpTo("row-20");
show("① 跳转到 row-20（估计高度布局）");

// 测量锚点上方 5 行 + 当前可见行：上方行高修正会同步调整 scrollTop
for (let i = 15; i < 20; i++) requestAsyncMeasure(`row-${i}`);
for (const id of list.visibleRange().ids) requestAsyncMeasure(id);
const beforeMeasure = list.scrollTop;
const applied = flushMeasures();
console.log(
  `② 异步实测 ${applied} 行（含锚点上方 5 行）：scrollTop ${beforeMeasure} -> ${list.scrollTop}，锚点视觉位置不变`,
);
show("");

// 3) 锚点之前插入 100px
const before = list.scrollTop;
list.insert(0, { id: "banner", estimate: 100 });
console.log(`③ 锚点之前插入 100px 横幅：scrollTop ${before} -> ${list.scrollTop}（+${list.scrollTop - before}）`);

// 4) 删除锚点条目 → 后备到后继
const anchorId = list.getAnchor()!.id;
list.remove(anchorId);
console.log(`④ 删除锚点条目 ${anchorId} → 锚点后备到后继 ${list.getAnchor()!.id}，scrollTop=${list.scrollTop}`);

// 5) 折叠分组（产生零高行）
list.setCollapsed("group-A", true);
show("⑤ 折叠 group-A（4 个子项变为零高行）");

// 6) 迟到旧测量：重排前签发的令牌在重排后到达，应被忽略
const staleToken = model.requestMeasure("row-5")!;
list.move("row-5", 40); // 重排使令牌过期
const accepted = list.applyMeasure(staleToken, 999);
console.log(`⑥ 重排后到达的迟到旧测量被忽略：applyMeasure -> ${accepted}，row-5 高度仍为 ${model.storedHeightOf("row-5")}`);

// 7) 反向滚动 + 跳转
list.onScroll(list.scrollTop - 120);
show("⑦ 反向滚动 120px");
list.jumpTo("row-55");
show("⑧ 跳转到 row-55");
