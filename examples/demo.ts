/**
 * Runnable sample: `node --experimental-strip-types examples/demo.ts`
 * (Node >= 22.6; on Node >= 23.6 the flag can be omitted.)
 */
import { batch, h, mount, reactive, toHTML, when } from "../src/index.ts";

const user = reactive({ name: "世界", vip: false });

let appRenderCount = 0;

function App() {
  appRenderCount++;
  return h(
    "div",
    { id: "app" },
    h("p", { class: () => (user.vip ? "vip" : null) }, () => `欢迎，${user.name}`),
    h("span", null, "静态内容：永远不会被重新计算"),
    h("footer", null, when(
      () => user.vip,
      () => h("em", null, "VIP 专属"),
    )),
  );
}

const engine = mount(h(App));

console.log("== 初始渲染 ==");
console.log(toHTML(engine.root));
console.log("挂载时产生的补丁（应为空）:", engine.drainPatches());

console.log("\n== user.name = '李四' ==");
user.name = "李四";
console.log("生成的补丁:", JSON.stringify(engine.drainPatches(), null, 2));
console.log("更新后的界面:", toHTML(engine.root));

console.log("\n== user.vip = true（属性 + 结构补丁） ==");
user.vip = true;
for (const patch of engine.drainPatches()) {
  console.log("补丁:", patch.op, patch.target, "node" in patch ? toHTML(patch.node) : "");
}
console.log("更新后的界面:", toHTML(engine.root));

console.log("\n== batch: name + vip 同时变化 ==");
batch(() => {
  user.name = "王五";
  user.vip = false;
});
for (const patch of engine.drainPatches()) {
  console.log("补丁:", patch.op, patch.target, "node" in patch ? toHTML(patch.node) : "");
}
console.log("更新后的界面:", toHTML(engine.root));

console.log("\n== 组件函数执行次数（始终为 1，证明没有全树重渲染/Diff） ==:");
console.log(appRenderCount);

engine.dispose();
