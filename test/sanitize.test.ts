import assert from "node:assert/strict";
import { test } from "node:test";
import { countElements, parseFragment, sanitizeFragment } from "../index.ts";

test("svg闭合后再写script: 裁决后脚本元素数量为0", () => {
  const fragment = `<svg viewBox="0 0 10 10"><circle r="4"/></svg><script>alert(1)</script>`;
  const out = sanitizeFragment(fragment);
  assert.equal(countElements(out, "script"), 0);
  assert.equal(countElements(out, "circle"), 1);
});

test("外来点中间的script不执行: svg/math内部的script被移除", () => {
  const out = sanitizeFragment(
    `<svg><script>alert(1)</script><rect width="8" height="8"/></svg>` +
      `<math><script>alert(2)</script><mi>x</mi></math>`,
  );
  assert.equal(countElements(out, "script"), 0);
  assert.equal(countElements(out, "rect"), 1);
  assert.equal(countElements(out, "mi"), 1);
});

test("集成点不回流: desc/title/foreignObject内的script仍属外来命名空间", () => {
  const out = sanitizeFragment(
    `<svg><desc><script>alert(1)</script></desc>` +
      `<foreignObject><script>alert(2)</script></foreignObject></svg>`,
  );
  assert.equal(countElements(out, "script"), 0);
});

test("嵌套svg提前闭合后, 剩余内容仍处外来命名空间", () => {
  const out = sanitizeFragment(
    `<svg><svg></svg><script>alert(1)</script><path d="M0 0h4"/></svg>`,
  );
  assert.equal(countElements(out, "script"), 0);
  assert.equal(countElements(out, "path"), 1);
});

test("纯HTML里的合法svg图形元素保留", () => {
  const out = sanitizeFragment(
    `<p>图例</p><svg viewBox="0 0 24 24"><g><circle cx="12" cy="12" r="10"/>` +
      `<path d="M2 12h20"/><text x="4" y="6">ok</text></g></svg>`,
  );
  for (const tag of ["svg", "g", "circle", "path", "text"]) {
    assert.equal(countElements(out, tag), 1, `${tag} 应保留`);
  }
  assert.match(out, /图例/);
});

test("进入svg后只在对应结束标签回到HTML命名空间", () => {
  const tree = parseFragment(`<svg><g><circle/></g></svg><div>after</div>`);
  const svg = tree[0];
  const div = tree[1];
  assert.equal(svg.type, "element");
  assert.equal(div.type, "element");
  if (svg.type === "element" && div.type === "element") {
    assert.equal(svg.ns, "svg");
    assert.equal(div.ns, "html");
  }
});

test("math同理: 对应结束标签前不回到HTML", () => {
  const out = sanitizeFragment(`<math><mtext></math><script>alert(1)</script>`);
  assert.equal(countElements(out, "script"), 0);
});
