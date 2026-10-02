import { test } from "node:test";
import assert from "node:assert/strict";
import {
  batch,
  findNode,
  h,
  mount,
  reactive,
  signal,
  toHTML,
  when,
} from "../src/index.ts";
import type { VText } from "../src/index.ts";

test("验收边界: user.name 变化直接产出 UpdateTextNode 精准补丁，无全树 Diff", () => {
  const user = reactive({ name: "世界" });
  let appRenderCount = 0;
  let greetingRenderCount = 0;

  function Greeting() {
    greetingRenderCount++;
    return h("p", null, () => `欢迎，${user.name}`);
  }
  function App() {
    appRenderCount++;
    return h(
      "div",
      { id: "app" },
      h(Greeting),
      h("span", null, "静态内容"),
    );
  }

  const engine = mount(h(App));
  assert.equal(toHTML(engine.root), '<div id="app"><p>欢迎，世界</p><span>静态内容</span></div>');
  assert.deepEqual(engine.drainPatches(), []);

  user.name = "李四";

  // 唯一直接定位到目标文本节点的最小补丁
  assert.deepEqual(engine.drainPatches(), [
    { op: "UpdateTextNode", target: "0/0/0", value: "欢迎，李四" },
  ]);
  // 界面已局部对齐
  assert.equal(toHTML(engine.root), '<div id="app"><p>欢迎，李四</p><span>静态内容</span></div>');
  // 组件树从未被重新执行或对比
  assert.equal(appRenderCount, 1);
  assert.equal(greetingRenderCount, 1);

  engine.dispose();
});

test("补丁 target 精准指向具体的虚拟文本节点", () => {
  const name = signal("A");
  const engine = mount(
    h("div", null,
      h("p", null, "静态"),
      h("p", null, () => `欢迎，${name.get()}`),
    ),
  );

  name.set("B");
  const patches = engine.drainPatches();
  assert.equal(patches.length, 1);
  assert.equal(patches[0]!.op, "UpdateTextNode");

  // target 解析到的正是绑定所在的文本节点
  const target = findNode(engine.root, patches[0]!.target) as VText;
  assert.equal(target.kind, "text");
  assert.equal(target.value, "欢迎，B");
  // 兄弟静态文本节点未被触碰
  const sibling = findNode(engine.root, "0/0/0") as VText;
  assert.equal(sibling.value, "静态");

  engine.dispose();
});

test("属性绑定产出 SetAttribute / RemoveAttribute 补丁", () => {
  const vip = signal(false);
  const engine = mount(h("p", { class: () => (vip.get() ? "vip" : null) }, "hi"));
  assert.equal(toHTML(engine.root), "<p>hi</p>");

  vip.set(true);
  assert.deepEqual(engine.drainPatches(), [
    { op: "SetAttribute", target: "0", name: "class", value: "vip" },
  ]);
  assert.equal(toHTML(engine.root), '<p class="vip">hi</p>');

  vip.set(false);
  assert.deepEqual(engine.drainPatches(), [
    { op: "RemoveAttribute", target: "0", name: "class" },
  ]);
  assert.equal(toHTML(engine.root), "<p>hi</p>");

  engine.dispose();
});

test("无关信号变化不产生任何补丁", () => {
  const shown = signal("显示");
  const unrelated = signal(0);
  const engine = mount(h("p", null, () => shown.get()));

  unrelated.set(1);
  unrelated.set(2);
  assert.deepEqual(engine.drainPatches(), []);

  engine.dispose();
});

test("相同值不产生补丁；一个信号驱动多个绑定时各自精准更新", () => {
  const name = signal("世界");
  const engine = mount(
    h("div", null,
      h("p", null, () => `欢迎，${name.get()}`),
      h("h1", null, () => `你好，${name.get()}`),
    ),
  );

  name.set("世界"); // 相同值
  assert.deepEqual(engine.drainPatches(), []);

  name.set("李四");
  assert.deepEqual(engine.drainPatches(), [
    { op: "UpdateTextNode", target: "0/0/0", value: "欢迎，李四" },
    { op: "UpdateTextNode", target: "0/1/0", value: "你好，李四" },
  ]);

  engine.dispose();
});

test("batch 内多次写入只产生一次最小补丁", () => {
  const name = signal("A");
  const engine = mount(h("p", null, () => `欢迎，${name.get()}`));

  batch(() => {
    name.set("B");
    name.set("C");
    name.set("D");
  });
  assert.deepEqual(engine.drainPatches(), [
    { op: "UpdateTextNode", target: "0/0", value: "欢迎，D" },
  ]);

  engine.dispose();
});

test("when() 条件插槽产出 InsertNode / RemoveNode 结构补丁", () => {
  const vip = signal(false);
  const engine = mount(
    h("footer", null, when(
      () => vip.get(),
      () => h("em", null, "VIP 专属"),
    )),
  );
  assert.equal(toHTML(engine.root), "<footer></footer>");

  vip.set(true);
  const inserts = engine.drainPatches();
  assert.equal(inserts.length, 1);
  assert.equal(inserts[0]!.op, "InsertNode");
  assert.equal(inserts[0]!.target, "0/0");
  assert.equal(toHTML(engine.root), "<footer><em>VIP 专属</em></footer>");

  vip.set(false);
  assert.deepEqual(engine.drainPatches(), [{ op: "RemoveNode", target: "0/0" }]);
  assert.equal(toHTML(engine.root), "<footer></footer>");

  engine.dispose();
});

test("被移除分支内的绑定随分支一起销毁", () => {
  const vip = signal(true);
  const name = signal("世界");
  const engine = mount(
    h("footer", null, when(
      () => vip.get(),
      () => h("em", null, () => `VIP ${name.get()}`),
    )),
  );
  assert.equal(toHTML(engine.root), "<footer><em>VIP 世界</em></footer>");

  vip.set(false);
  engine.drainPatches();

  // 分支已移除，其内部绑定不再响应
  name.set("李四");
  assert.deepEqual(engine.drainPatches(), []);

  engine.dispose();
});

test("dispose 之后不再产生补丁", () => {
  const name = signal("世界");
  const engine = mount(h("p", null, () => `欢迎，${name.get()}`));
  engine.dispose();

  name.set("李四");
  assert.deepEqual(engine.drainPatches(), []);
});

test("静态模板挂载时不产生补丁", () => {
  const engine = mount(
    h("div", { id: "a", hidden: false },
      h("span", null, "纯静态"),
      42,
      null,
    ),
  );
  assert.deepEqual(engine.drainPatches(), []);
  assert.equal(toHTML(engine.root), '<div id="a"><span>纯静态</span>42</div>');
  engine.dispose();
});
