import { test } from "node:test";
import assert from "node:assert/strict";
import { NodeTree } from "../src/tree.ts";
import { Scheduler } from "../src/scheduler.ts";
import { LayerManager } from "../src/layers.ts";
import { layoutTree, hitTest } from "../src/layout.ts";
import { measureText } from "../src/glyphs.ts";
import {
  glyphMetrics,
  layoutOptions,
  pageSpec,
  settingsLayerSpec,
  confirmLayerSpec,
  helpLayerSpec,
} from "../src/fixtures.ts";

function makeWorld() {
  const tree = new NodeTree(pageSpec());
  const scheduler = new Scheduler();
  const mgr = new LayerManager(tree, scheduler);
  return { tree, scheduler, mgr };
}

test("嵌套打开:焦点限制在顶层,背景 inert 按引用计数叠加", () => {
  const { tree, scheduler, mgr } = makeWorld();
  mgr.open({ id: "settings", content: settingsLayerSpec(), triggerId: "btn-settings" });
  scheduler.flush();
  assert.equal(mgr.focus.active, "input-name"); // autofocus 初始焦点
  assert.equal(mgr.inert.isInert("btn-settings", tree), true);
  assert.equal(mgr.inert.isInert("input-name", tree), false);
  assert.deepEqual(mgr.inert.snapshot(), { page: 1 });

  mgr.open({ id: "confirm", content: confirmLayerSpec(), triggerId: "btn-open-confirm" });
  scheduler.flush();
  assert.equal(mgr.focus.active, "btn-yes"); // 只有顶层拥有焦点
  assert.equal(mgr.inert.isInert("input-name", tree), true); // 外层同样被隔离
  assert.deepEqual(mgr.inert.snapshot(), { "layer-settings": 1, page: 2 });
});

test("关闭内层不解除外层背景隔离", () => {
  const { tree, scheduler, mgr } = makeWorld();
  mgr.open({ id: "settings", content: settingsLayerSpec(), triggerId: "btn-settings" });
  mgr.open({ id: "confirm", content: confirmLayerSpec(), triggerId: "btn-open-confirm" });
  scheduler.flush();
  mgr.close("confirm");
  scheduler.flush();
  assert.equal(mgr.focus.active, "btn-open-confirm"); // 焦点还给内层触发者
  assert.equal(mgr.inert.isInert("input-name", tree), false); // 设置层恢复可交互
  assert.equal(mgr.inert.isInert("btn-settings", tree), true); // 页面仍被外层隔离
  assert.deepEqual(mgr.inert.snapshot(), { page: 1 });
});

test("关闭恢复触发者;触发者已删除时回到合法后备", () => {
  const { tree, scheduler, mgr } = makeWorld();
  mgr.open({ id: "settings", content: settingsLayerSpec(), triggerId: "btn-settings" });
  scheduler.flush();
  mgr.close("settings");
  scheduler.flush();
  assert.equal(mgr.focus.active, "btn-settings"); // 正常恢复触发者

  mgr.open({ id: "settings", content: settingsLayerSpec(), triggerId: "btn-settings" });
  scheduler.flush();
  tree.remove("btn-settings"); // 触发者被删除
  mgr.close("settings");
  scheduler.flush();
  assert.equal(mgr.focus.active, "btn-help"); // 后备:范围内首个可聚焦节点
});

test("焦点恢复允许异步挂载:目标未挂载时挂起,挂载后恢复", () => {
  const tree = new NodeTree({
    id: "app",
    children: [
      { id: "page", children: [{ id: "btn-lonely", text: "Go", focusable: true }] },
    ],
  });
  const scheduler = new Scheduler();
  const mgr = new LayerManager(tree, scheduler);
  mgr.open({
    id: "modal",
    triggerId: "btn-lonely",
    content: { id: "layer-m", children: [{ id: "btn-m", text: "OK", focusable: true }] },
  });
  scheduler.flush();
  assert.equal(mgr.focus.active, "btn-m");
  tree.remove("btn-lonely"); // 触发者删除,页面再无候选
  mgr.close("modal");
  scheduler.flush();
  assert.equal(mgr.focus.active, null); // 恢复请求挂起而非错误落点
  tree.mount("page", { id: "btn-late", text: "Late", focusable: true }); // 异步挂载
  scheduler.flush();
  assert.equal(mgr.focus.active, "btn-late");
});

test("分代提交:关闭、触发者删除与新层打开交错,过时请求不得夺焦", () => {
  const { tree, scheduler, mgr } = makeWorld();
  mgr.open({ id: "settings", content: settingsLayerSpec(), triggerId: "btn-settings" });
  scheduler.flush();
  mgr.open({ id: "confirm", content: confirmLayerSpec(), triggerId: "btn-open-confirm" });
  scheduler.flush();

  mgr.close("confirm"); // 恢复请求按代 g 提交,尚未执行
  mgr.open({ id: "help", content: helpLayerSpec(), triggerId: "btn-open-confirm" }); // 代 g+1
  scheduler.flush();
  assert.equal(mgr.focus.active, "btn-close-help"); // 过时的恢复没有夺回焦点

  tree.remove("btn-open-confirm"); // 帮助层触发者被删除(交错)
  mgr.close("help");
  scheduler.flush();
  assert.equal(mgr.focus.active, "input-name"); // 后备到设置层 autofocus
});

test("重复关闭与重复 finalize 幂等,引用最终全部释放", () => {
  const { scheduler, mgr } = makeWorld();
  mgr.open({ id: "settings", content: settingsLayerSpec(), triggerId: "btn-settings", exitAnimation: true });
  scheduler.flush();
  assert.equal(mgr.close("settings"), true);
  assert.equal(mgr.close("settings"), false); // 重复关闭:no-op
  assert.equal(mgr.finalize("settings"), true);
  assert.equal(mgr.finalize("settings"), false); // 重复收尾:no-op
  scheduler.flush();
  assert.equal(mgr.inert.totalRefs, 0);
  assert.deepEqual(mgr.inert.snapshot(), {});
});

test("Escape 只关闭可关闭的顶层,不向下穿透", () => {
  const { scheduler, mgr } = makeWorld();
  mgr.open({ id: "settings", content: settingsLayerSpec(), triggerId: "btn-settings" });
  mgr.open({ id: "help", content: helpLayerSpec(), triggerId: "btn-help", dismissible: false });
  scheduler.flush();
  assert.equal(mgr.escape(), false); // 顶层不可关闭:不穿透到设置层
  assert.equal(mgr.topLayerId(), "help");
  mgr.close("help");
  scheduler.flush();
  assert.equal(mgr.escape(), true); // 现在顶层是可关闭的设置层
  assert.equal(mgr.topLayerId(), null);
});

test("退出动画存活期间可交互范围明确,closing 层不可交互", () => {
  const { tree, scheduler, mgr } = makeWorld();
  mgr.open({ id: "settings", content: settingsLayerSpec(), triggerId: "btn-settings" });
  mgr.open({ id: "confirm", content: confirmLayerSpec(), triggerId: "btn-open-confirm", exitAnimation: true });
  scheduler.flush();
  mgr.close("confirm");
  // 动画存活:层仍在树上,但明确不可交互
  assert.equal(tree.has("layer-confirm"), true);
  assert.deepEqual(mgr.closingRoots(), ["layer-confirm"]);
  assert.equal(mgr.interactiveScope().rootId, "layer-settings");
  assert.equal(mgr.isInteractive("btn-yes"), false);
  assert.equal(mgr.isInteractive("input-name"), true);
  assert.equal(mgr.focus.isFocusable("btn-yes"), false);
  const rects = layoutTree(tree, glyphMetrics, layoutOptions);
  const yesRect = rects.get("btn-yes")!;
  assert.equal(mgr.dispatchPointer(yesRect.x + 1, yesRect.y + 1, rects), null); // 事件被吞
  mgr.finalize("confirm");
  scheduler.flush();
  assert.equal(tree.has("layer-confirm"), false);
  assert.equal(mgr.focus.active, "btn-open-confirm");
  assert.equal(mgr.inert.totalRefs, 0 + 1); // 外层对页面的引用仍在
});

test("无弹层时退出动画中的层同样被排除在文档可交互范围外", () => {
  const { scheduler, mgr } = makeWorld();
  mgr.open({ id: "confirm", content: confirmLayerSpec(), triggerId: "btn-settings", exitAnimation: true });
  scheduler.flush();
  mgr.close("confirm");
  assert.equal(mgr.interactiveScope().kind, "document");
  assert.equal(mgr.isInteractive("btn-yes"), false); // closing 层被自我隔离
  assert.equal(mgr.isInteractive("btn-help"), true); // 页面立即可交互
  mgr.finalize("confirm");
  scheduler.flush();
  assert.equal(mgr.focus.active, "btn-settings");
  assert.equal(mgr.inert.totalRefs, 0);
});

test("焦点陷阱:Tab 循环被限制在顶层并回绕", () => {
  const { scheduler, mgr } = makeWorld();
  mgr.open({ id: "settings", content: settingsLayerSpec(), triggerId: "btn-settings" });
  scheduler.flush();
  assert.equal(mgr.focus.active, "input-name");
  assert.equal(mgr.focus.focusNext(), "btn-open-confirm");
  assert.equal(mgr.focus.focusNext(), "btn-close-settings");
  assert.equal(mgr.focus.focusNext(), "input-name"); // 回绕,不落到页面
  assert.equal(mgr.focus.focusPrev(), "btn-close-settings");
  // 范围外的直接聚焦请求被拒绝
  assert.equal(mgr.focus.focus("btn-help"), false);
  assert.equal(mgr.focus.active, "btn-close-settings");
});

test("字形度量与布局:核心计算不借浏览器布局引擎", () => {
  assert.equal(measureText("设置面板", glyphMetrics), 8); // 4 个 CJK × 2
  assert.equal(measureText("Open", glyphMetrics), 4); // 后备宽度 1 × 4
  const tree = new NodeTree({
    id: "app",
    children: [
      {
        id: "page",
        children: [
          { id: "a", text: "设", focusable: true },
          { id: "b", text: "ab", focusable: true, hidden: true },
          { id: "c", text: "abc", focusable: true },
        ],
      },
    ],
  });
  const rects = layoutTree(tree, glyphMetrics, layoutOptions);
  assert.equal(rects.get("a")!.width, 2 + layoutOptions.padding * 2);
  assert.equal(rects.has("b"), false); // 隐藏节点不参与布局
  const rectC = rects.get("c")!;
  assert.equal(hitTest(rects, tree.documentOrder(), rectC.x + 1, rectC.y + 1), "c");
  assert.equal(hitTest(rects, tree.documentOrder(), 9999, 9999), null);
});
