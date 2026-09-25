/**
 * 运行样例:嵌套模态的焦点限制、inert 引用计数、分代焦点请求、
 * 触发者删除后备、退出动画期间的可交互范围、异步挂载恢复。
 * 运行:npm run sample(需要 Node >= 22.6)
 */
import { NodeTree } from "./tree.ts";
import { Scheduler } from "./scheduler.ts";
import { LayerManager } from "./layers.ts";
import { layoutTree } from "./layout.ts";
import { measureText } from "./glyphs.ts";
import {
  glyphMetrics,
  layoutOptions,
  pageSpec,
  settingsLayerSpec,
  confirmLayerSpec,
  helpLayerSpec,
} from "./fixtures.ts";

const tree = new NodeTree(pageSpec());
const scheduler = new Scheduler();
const mgr = new LayerManager(tree, scheduler);

function show(label: string): void {
  const scope = mgr.interactiveScope();
  console.log(`\n== ${label}`);
  console.log(`  focus      : ${mgr.focus.active ?? "(none)"}`);
  console.log(`  scope      : ${scope.kind} -> ${scope.rootId}`);
  console.log(`  inert refs : ${JSON.stringify(mgr.inert.snapshot())} (total=${mgr.inert.totalRefs})`);
}

console.log("== 字形度量与布局(夹具数据,纯计算)");
console.log(`  "设置面板" width      = ${measureText("设置面板", glyphMetrics)}`);
console.log(`  "Open Settings" width = ${measureText("Open Settings", glyphMetrics)}`);

let rects = layoutTree(tree, glyphMetrics, layoutOptions);
const helpRect = rects.get("btn-help")!;
const helpPoint = { x: helpRect.x + 1, y: helpRect.y + 1 };
console.log(`  btn-help rect = ${JSON.stringify(helpRect)}`);
console.log(`  弹层前指针命中: ${mgr.dispatchPointer(helpPoint.x, helpPoint.y, rects)}`);

// 1. 打开设置层:焦点进入顶层,页面 inert
mgr.open({ id: "settings", content: settingsLayerSpec(), triggerId: "btn-settings" });
scheduler.flush();
show("1. 打开设置层");
rects = layoutTree(tree, glyphMetrics, layoutOptions);
console.log(`  弹层后指针命中页面按钮: ${mgr.dispatchPointer(helpPoint.x, helpPoint.y, rects)}`);

// 2. 嵌套打开确认层:设置层也被隔离,引用计数叠加
mgr.open({ id: "confirm", content: confirmLayerSpec(), triggerId: "btn-open-confirm", exitAnimation: true });
scheduler.flush();
show("2. 嵌套打开确认层(退出动画开启)");

// 3. Escape 关闭确认层:退出动画存活期间,可交互范围明确为设置层
mgr.escape();
show("3. Escape 关闭确认层(退出动画存活中)");
console.log(`  closing 中的层根: ${JSON.stringify(mgr.closingRoots())}`);
console.log(`  btn-yes 可交互? ${mgr.isInteractive("btn-yes")}`);
console.log(`  指针命中确认层按钮: ${mgr.dispatchPointer(0, 0, rects) ?? "(被吞掉)"}`);
mgr.finalize("confirm");
scheduler.flush();
show("3b. 动画结束 finalize(焦点恢复触发者,设置层解除隔离)");

// 4. 交错:关闭确认层后、恢复执行前又打开帮助层 → 过时请求不得夺焦
mgr.open({ id: "confirm", content: confirmLayerSpec(), triggerId: "btn-open-confirm" });
scheduler.flush();
mgr.close("confirm"); // 恢复请求已按代 g 提交,尚未执行
mgr.open({ id: "help", content: helpLayerSpec(), triggerId: "btn-open-confirm", dismissible: false });
scheduler.flush(); // 代 g 的请求被丢弃,焦点属于帮助层
show("4. 关闭确认与打开帮助交错(过时的恢复请求被丢弃)");

// 5. 触发者删除 → 关闭时回到合法后备
tree.remove("btn-open-confirm"); // 帮助层的触发者被删除
mgr.close("help");
scheduler.flush();
show("5. 触发者已删除,焦点回到后备(设置层 autofocus)");

// 6. 关闭设置层,其触发者也已删除 → 后备到页面首个可聚焦节点
tree.remove("btn-settings");
mgr.close("settings");
scheduler.flush();
show("6. 设置层关闭,触发者已删除,后备到页面");

// 7. 异步挂载恢复:页面无可聚焦节点时挂起,挂载后恢复
mgr.open({ id: "help", content: helpLayerSpec(), triggerId: "btn-help" });
scheduler.flush();
tree.remove("btn-help");
tree.remove("btn-delete");
mgr.close("help");
scheduler.flush();
show("7. 关闭后页面暂无可聚焦节点(恢复请求挂起)");
tree.mount("page", { id: "btn-restored", text: "恢复按钮", focusable: true });
scheduler.flush();
show("7b. 后备节点异步挂载完成,焦点恢复");

console.log(`\n== 终态: inert 引用全部释放? ${mgr.inert.totalRefs === 0}`);
