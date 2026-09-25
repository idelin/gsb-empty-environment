import { test } from "node:test";
import assert from "node:assert/strict";
import { ListModel } from "../src/list-model.ts";
import { VirtualList } from "../src/scroller.ts";

function makeList(count: number, height = 40, viewport = 100): VirtualList {
  const model = new ListModel(height);
  const list = new VirtualList(model, viewport);
  for (let i = 0; i < count; i++) model.append({ id: `row-${i}` });
  return list;
}

test("锚点之前新增 100 像素，scrollTop 同步 +100", () => {
  const list = makeList(10);
  list.onScroll(200); // 锚点落在 row-5，offset 0
  assert.deepEqual(list.getAnchor(), { id: "row-5", offset: 0 });
  list.insert(0, { id: "banner", estimate: 100 });
  assert.equal(list.scrollTop, 300);
  assert.deepEqual(list.getAnchor(), { id: "row-5", offset: 0 });
  // 锚点之后插入不影响滚动位置
  list.insert(9, { id: "tail", estimate: 100 });
  assert.equal(list.scrollTop, 300);
});

test("锚点条目被删除时选择后继，无后继时选择前驱，空表清空锚点", () => {
  const list = makeList(10, 40, 30);
  list.onScroll(200);
  list.remove("row-5");
  assert.deepEqual(list.getAnchor(), { id: "row-6", offset: 0 });
  assert.equal(list.scrollTop, 200); // 后继滑入原位置，视口不动

  list.onScroll(330); // 落末行 row-9（320..360），offset 10
  assert.deepEqual(list.getAnchor(), { id: "row-9", offset: 10 });
  list.remove("row-9"); // 无后继 → 前驱 row-8
  assert.deepEqual(list.getAnchor(), { id: "row-8", offset: 0 });
  assert.equal(list.scrollTop, 290); // 夹取到新的 maxScroll

  for (const id of list.model.ids()) list.remove(id);
  assert.equal(list.getAnchor(), null);
  assert.equal(list.scrollTop, 0);
});

test("锚点上方的异步实测修正同步调整 scrollTop，下方不影响", () => {
  const list = makeList(10);
  list.onScroll(200);
  const above = list.model.requestMeasure("row-2")!;
  const below = list.model.requestMeasure("row-8")!;
  assert.equal(list.applyMeasure(above, 90), true); // +50
  assert.equal(list.scrollTop, 250);
  assert.equal(list.applyMeasure(below, 400), true);
  assert.equal(list.scrollTop, 250);
  assert.deepEqual(list.getAnchor(), { id: "row-5", offset: 0 });
});

test("迟到旧测量（重排后令牌过期 / 条目已删）被忽略", () => {
  const list = makeList(10);
  list.onScroll(200);
  const stale = list.model.requestMeasure("row-1")!;
  list.move("row-1", 7); // 重排使 epoch 失效
  assert.equal(list.applyMeasure(stale, 999), false);
  assert.equal(list.model.storedHeightOf("row-1"), 40);

  const doomed = list.model.requestMeasure("row-3")!;
  list.remove("row-3");
  assert.equal(list.applyMeasure(doomed, 999), false);

  const fresh = list.model.requestMeasure("row-1")!;
  assert.equal(list.applyMeasure(fresh, 70), true);
  assert.equal(list.model.storedHeightOf("row-1"), 70);
});

test("折叠分组产生零高行，查找与可见集正常且锚点稳定", () => {
  const model = new ListModel(40);
  const list = new VirtualList(model, 100);
  model.append({ id: "g1" });
  for (let i = 0; i < 5; i++) model.append({ id: `g1-c${i}`, parentId: "g1" });
  for (let i = 0; i < 10; i++) model.append({ id: `row-${i}` });
  // 总高 40*16 = 640；跳到分组之后
  list.jumpTo("row-2");
  const before = list.scrollTop;
  list.setCollapsed("g1", true); // 锚点上方 5 行变为零高：-200
  assert.equal(list.scrollTop, before - 200);
  assert.equal(list.getAnchor()!.id, "row-2");
  const range = list.visibleRange();
  assert.ok(range.ids.includes("row-2"));
  list.setCollapsed("g1", false);
  assert.equal(list.scrollTop, before);
});

test("折叠期间到达的有效测量被记录，展开后生效", () => {
  const model = new ListModel(40);
  const list = new VirtualList(model, 100);
  model.append({ id: "g" });
  model.append({ id: "child", parentId: "g" });
  model.append({ id: "row" });
  list.setCollapsed("g", true);
  assert.equal(list.model.heightOf("child"), 0);
  const token = list.model.requestMeasure("child")!;
  assert.equal(list.applyMeasure(token, 77), true);
  assert.equal(list.model.heightOf("child"), 0); // 仍折叠
  assert.equal(list.model.storedHeightOf("child"), 77);
  list.setCollapsed("g", false);
  assert.equal(list.model.heightOf("child"), 77);
});

test("反向滚动与跳转：锚点随滚动重建", () => {
  const list = makeList(100);
  list.jumpTo("row-50");
  assert.equal(list.scrollTop, 2000);
  assert.deepEqual(list.getAnchor(), { id: "row-50", offset: 0 });
  list.onScroll(1930); // 反向滚动
  assert.deepEqual(list.getAnchor(), { id: "row-48", offset: 10 });
  list.onScroll(5000); // 正向大幅滚动，夹取到 maxScroll
  assert.equal(list.scrollTop, 4000 - 100);
  list.jumpTo("missing");
  assert.equal(list.scrollTop, 3900); // 失败跳转无副作用
  list.jumpToOffset(0);
  assert.deepEqual(list.getAnchor(), { id: "row-0", offset: 0 });
});

test("零高行不会导致可见集计算死循环", () => {
  const model = new ListModel(0); // 全部零高
  const list = new VirtualList(model, 100);
  for (let i = 0; i < 50; i++) model.append({ id: `z-${i}` });
  list.onScroll(0);
  const range = list.visibleRange();
  assert.equal(range.start, 50); // findIndex 直达末尾
  assert.deepEqual(range.ids, []);
  model.append({ id: "real", estimate: 30 });
  list.onScroll(0);
  assert.deepEqual(list.visibleRange().ids, ["real"]);
});

test("重排保持锚点视觉位置", () => {
  const list = makeList(20);
  list.onScroll(400); // 锚点 row-10
  list.move("row-2", 15); // 锚点上方少一行 → -40
  assert.equal(list.scrollTop, 360);
  assert.deepEqual(list.getAnchor(), { id: "row-10", offset: 0 });
  list.move("row-18", 0); // 锚点上方多一行 → +40
  assert.equal(list.scrollTop, 400);
});
