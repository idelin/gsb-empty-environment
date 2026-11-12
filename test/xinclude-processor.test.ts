import assert from "node:assert/strict";
import { test } from "node:test";
import {
  MapResourceResolver,
  XIncludeProcessor,
} from "../src/index.ts";
import {
  XIncludeCycleError,
  XIncludeLimitError,
  XIncludeResourceError,
  XIncludeSecurityError,
} from "../src/errors.ts";

const XI = "http://www.w3.org/2001/XInclude";

function makeProcessor(
  entries: Record<string, string>,
  options: { maxDepth?: number; maxExpandedBytes?: number } = {},
): { processor: XIncludeProcessor; resolver: MapResourceResolver } {
  const resolver = new MapResourceResolver(entries);
  const processor = new XIncludeProcessor({ resolver, ...options });
  return { processor, resolver };
}

test("换任意命名空间前缀仍按 URI 识别 include 并展开", async () => {
  const { processor, resolver } = makeProcessor({
    "frag/b.xml": `<b>inside</b>`,
  });
  const root =
    `<r xmlns:totally-not-xi="${XI}">` +
    `<totally-not-xi:include href="frag/b.xml"/>` +
    `</r>`;
  const result = await processor.process(root);
  assert.match(result.xml, /<b>inside<\/b>/);
  assert.equal(resolver.callCount, 1);
  assert.equal(resolver.callLog[0].key, "frag/b.xml");
});

test("通过默认命名空间声明的 include 同样被识别", async () => {
  const { processor, resolver } = makeProcessor({
    "b.xml": `<b>default-ns</b>`,
  });
  const root =
    `<r xmlns="${XI}"><include href="b.xml"/></r>`;
  const result = await processor.process(root);
  assert.match(result.xml, /<b>default-ns<\/b>/);
  assert.equal(resolver.callCount, 1);
});

test("仅前缀匹配但 URI 错误的 include 不触发解析器", async () => {
  const { processor, resolver } = makeProcessor({ "b.xml": "<b/>" });
  const root =
    `<r xmlns:xi="urn:wrong"><xi:include href="b.xml"/></r>`;
  const result = await processor.process(root);
  assert.match(result.xml, /xi:include/);
  assert.equal(resolver.callCount, 0);
});

test("绝对路径与 file: href 被拒绝且解析器零回调", async () => {
  const attacks = [
    `<xi:include href="/etc/passwd"/>`,
    `<xi:include href="file:///etc/passwd"/>`,
    `<xi:include href="http://evil.test/x.xml"/>`,
    `<xi:include href="../../escape.xml"/>`,
    `<xi:include href="a\\b.xml"/>`,
    `<xi:include href="C:/windows/win.ini"/>`,
    `<xi:include href="%2e%2e/%2e%2e/etc/passwd"/>`,
  ];
  for (const attack of attacks) {
    const { processor, resolver } = makeProcessor({ "b.xml": "<b/>" });
    const root = `<r xmlns:xi="${XI}">${attack}</r>`;
    await assert.rejects(() => processor.process(root), XIncludeSecurityError);
    assert.equal(resolver.callCount, 0, attack);
  }
});

test("安全策略错误不会被 xi:fallback 吞掉", async () => {
  const { processor, resolver } = makeProcessor({});
  const root =
    `<r xmlns:xi="${XI}">` +
    `<xi:include href="/etc/passwd"><xi:fallback><safe/></xi:fallback></xi:include>` +
    `</r>`;
  await assert.rejects(() => processor.process(root), XIncludeSecurityError);
  assert.equal(resolver.callCount, 0);
});

test("未登记资源走 fallback，回调恰好一次", async () => {
  const { processor, resolver } = makeProcessor({});
  const root =
    `<r xmlns:xi="${XI}">` +
    `<xi:include href="missing.xml"><xi:fallback><fb>兜底</fb></xi:fallback></xi:include>` +
    `</r>`;
  const result = await processor.process(root);
  assert.match(result.xml, /<fb>兜底<\/fb>/);
  assert.equal(resolver.callCount, 1);
});

test("未登记资源且无 fallback 时抛资源错误", async () => {
  const { processor } = makeProcessor({});
  const root = `<r xmlns:xi="${XI}"><xi:include href="missing.xml"/></r>`;
  await assert.rejects(() => processor.process(root), XIncludeResourceError);
});

test("parse=text 将内容内联为转义文本，无法注入标记", async () => {
  const { processor, resolver } = makeProcessor({
    "snippet.txt": `<evil attr="x">& <script>alert(1)</script>`,
  });
  const root =
    `<r xmlns:xi="${XI}"><xi:include href="snippet.txt" parse="text"/></r>`;
  const result = await processor.process(root);
  assert.doesNotMatch(result.xml, /<evil/);
  assert.match(result.xml, /&lt;evil/);
  assert.match(result.xml, /&amp; &lt;script&gt;/);
  assert.equal(resolver.callCount, 1);
});

test("非法 parse 模式按安全错误拒绝", async () => {
  const { processor, resolver } = makeProcessor({});
  const root = `<r xmlns:xi="${XI}"><xi:include href="x" parse="xhtml"/></r>`;
  await assert.rejects(() => processor.process(root), XIncludeSecurityError);
  assert.equal(resolver.callCount, 0);
});

test("嵌套包含按同包相对路径逐层解析并统计回调次数", async () => {
  const { processor, resolver } = makeProcessor({
    "level1.xml": `<l1 xmlns:xi="${XI}"><xi:include href="sub/level2.xml"/></l1>`,
    "sub/level2.xml": `<l2 xmlns:xi="${XI}"><xi:include href="../level3.xml"/></l2>`,
    "level3.xml": `<l3>deep</l3>`,
  });
  const root = `<r xmlns:xi="${XI}"><xi:include href="level1.xml"/></r>`;
  const result = await processor.process(root);
  assert.match(result.xml, /<l1[ >]/);
  assert.match(result.xml, /<l2[ >]/);
  assert.match(result.xml, /<l3>deep<\/l3>/);
  assert.equal(resolver.callCount, 3);
  assert.deepEqual(
    resolver.callLog.map((c) => c.key),
    ["level1.xml", "sub/level2.xml", "level3.xml"],
  );
  assert.deepEqual(
    resolver.callLog.map((c) => c.depth),
    [1, 2, 3],
  );
});

test("循环引用被检测并抛 CycleError，回调次数等于首次触环次数", async () => {
  const { processor, resolver } = makeProcessor({
    "a.xml": `<a xmlns:xi="${XI}"><xi:include href="b.xml"/></a>`,
    "b.xml": `<b xmlns:xi="${XI}"><xi:include href="a.xml"/></b>`,
  });
  const root = `<r xmlns:xi="${XI}"><xi:include href="a.xml"/></r>`;
  await assert.rejects(() => processor.process(root), XIncludeCycleError);
  // a 与 b 各解析一次，再次回到 a 时在归一化后即识别环，不再回调。
  assert.equal(resolver.callCount, 2);
});

test("自包含循环同样被拦截", async () => {
  const { processor, resolver } = makeProcessor({
    "self.xml": `<s xmlns:xi="${XI}"><xi:include href="self.xml"/></s>`,
  });
  const root = `<r xmlns:xi="${XI}"><xi:include href="self.xml"/></r>`;
  await assert.rejects(() => processor.process(root), XIncludeCycleError);
  assert.equal(resolver.callCount, 1);
});

test("超过最大嵌套深度时拒绝继续展开", async () => {
  const chain: Record<string, string> = {};
  for (let i = 1; i <= 10; i += 1) {
    const next = i < 10 ? `n${i + 1}.xml` : null;
    chain[`n${i}.xml`] = next
      ? `<n${i} xmlns:xi="${XI}"><xi:include href="${next}"/></n${i}>`
      : `<n${i}>leaf</n${i}>`;
  }
  const { processor, resolver } = makeProcessor(chain, { maxDepth: 3 });
  const root = `<r xmlns:xi="${XI}"><xi:include href="n1.xml"/></r>`;
  await assert.rejects(() => processor.process(root), XIncludeResourceError);
  // 深度 1、2、3 各解析一次；尝试第 4 层前即中止。
  assert.equal(resolver.callCount, 3);
});

test("深度超限但配置 fallback 时可兜底", async () => {
  const chain = {
    "n1.xml": `<n1 xmlns:xi="${XI}"><xi:include href="n2.xml"/></n1>`,
    "n2.xml": `<n2 xmlns:xi="${XI}"><xi:include href="n3.xml"/></n2>`,
    "n3.xml": `<n3>leaf</n3>`,
  };
  const resolver = new MapResourceResolver(chain);
  const processor = new XIncludeProcessor({
    resolver,
    maxDepth: 2,
  });
  const root =
    `<r xmlns:xi="${XI}">` +
    `<xi:include href="n1.xml"><xi:fallback><fb/></xi:fallback></xi:include>` +
    `</r>`;
  const result = await processor.process(root);
  assert.match(result.xml, /<fb\/>/);
});

test("xml:base 不能改变允许范围，解析仍以文档自身位置为基准", async () => {
  const { processor, resolver } = makeProcessor({
    "frag/b.xml": `<b>scoped</b>`,
  });
  const root =
    `<r xmlns:xi="${XI}" xml:base="/etc/">` +
    `<xi:include xml:base="file:///nonexistent/" href="frag/b.xml"/>` +
    `</r>`;
  const result = await processor.process(root);
  assert.match(result.xml, /<b>scoped<\/b>/);
  assert.equal(resolver.callCount, 1);
  assert.equal(resolver.callLog[0].key, "frag/b.xml");
  // xml:base 在输出中被剥离。
  assert.doesNotMatch(result.xml, /xml:base=/);
});

test("xml:base 无法把相对 href 牵引到包外", async () => {
  const { processor, resolver } = makeProcessor({});
  const root =
    `<r xmlns:xi="${XI}">` +
    `<xi:include xml:base="/" href="etc/passwd"/>` +
    `</r>`;
  await assert.rejects(() => processor.process(root), XIncludeResourceError);
  // 归一化得到 etc/passwd（仍在包内命名空间），解析器被调用但登记缺失。
  assert.equal(resolver.callCount, 1);
  assert.equal(resolver.callLog[0].key, "etc/passwd");
});

test("展开字节数超限时中止且不静默截断", async () => {
  const { processor } = makeProcessor(
    {
      "big.xml": "x".repeat(500),
    },
    { maxExpandedBytes: 100 },
  );
  const root = `<r xmlns:xi="${XI}"><xi:include href="big.xml"/></r>`;
  await assert.rejects(() => processor.process(root), XIncludeLimitError);
});

test("同一资源多次包含会重复回调且都被展开", async () => {
  const { processor, resolver } = makeProcessor({
    "tile.xml": `<t>x</t>`,
  });
  const root =
    `<r xmlns:xi="${XI}">` +
    `<xi:include href="tile.xml"/><xi:include href="tile.xml"/>` +
    `</r>`;
  const result = await processor.process(root);
  assert.equal((result.xml.match(/<t>x<\/t>/g) ?? []).length, 2);
  assert.equal(resolver.callCount, 2);
});

test("合法同包片段的完整展开结果可被再次解析", async () => {
  const { processor, resolver } = makeProcessor({
    "parts/header.xml": `<header xmlns:xi="${XI}"><title><xi:include href="title.txt" parse="text"/></title></header>`,
    "parts/title.txt": `标题 & 公司`,
    "parts/body.xml": `<body>正文</body>`,
  });
  const root =
    `<doc xmlns:xi="${XI}">` +
    `<xi:include href="parts/header.xml"/>` +
    `<xi:include href="parts/body.xml"/>` +
    `</doc>`;
  const result = await processor.process(root);
  assert.match(result.xml, /<title>标题 &amp; 公司<\/title>/);
  assert.match(result.xml, /<body>正文<\/body>/);
  assert.equal(resolver.callCount, 3);
});

test("支持异步解析器，异常按资源错误处理并可 fallback", async () => {
  let calls = 0;
  const processor = new XIncludeProcessor({
    resolver: {
      async resolve() {
        calls += 1;
        throw new Error("存储后端暂时不可用");
      },
    },
  });
  const root =
    `<r xmlns:xi="${XI}">` +
    `<xi:include href="down.xml"><xi:fallback><fb/></xi:fallback></xi:include>` +
    `</r>`;
  const result = await processor.process(root);
  assert.match(result.xml, /<fb\/>/);
  assert.equal(calls, 1);
});

test("xpointer 属性按安全错误拒绝且零回调", async () => {
  const { processor, resolver } = makeProcessor({});
  const root = `<r xmlns:xi="${XI}"><xi:include href="a.xml" xpointer="xpointer(/r)"/></r>`;
  await assert.rejects(() => processor.process(root), XIncludeSecurityError);
  assert.equal(resolver.callCount, 0);
});

test("fallback 内的 include 被继续展开且单独计数", async () => {
  const { processor, resolver } = makeProcessor({
    "backup.xml": `<backup>兜底片段</backup>`,
  });
  const root =
    `<r xmlns:xi="${XI}">` +
    `<xi:include href="missing.xml">` +
    `<xi:fallback><xi:include href="backup.xml"/></xi:fallback>` +
    `</xi:include></r>`;
  const result = await processor.process(root);
  assert.match(result.xml, /<backup>兜底片段<\/backup>/);
  assert.equal(resolver.callCount, 2);
  assert.deepEqual(
    resolver.callLog.map((c) => c.key),
    ["missing.xml", "backup.xml"],
  );
});

test("空 fallback 使包含点静默消失", async () => {
  const { processor } = makeProcessor({});
  const root =
    `<r xmlns:xi="${XI}">a` +
    `<xi:include href="missing.xml"><xi:fallback/></xi:include>` +
    `b</r>`;
  const result = await processor.process(root);
  assert.match(result.xml, /<r[^>]*>ab<\/r>/);
});

test("include 内出现非 fallback 元素按安全错误拒绝", async () => {
  const { processor, resolver } = makeProcessor({ "a.xml": "<a/>" });
  const root =
    `<r xmlns:xi="${XI}">` +
    `<xi:include href="a.xml"><not-fallback/></xi:include></r>`;
  await assert.rejects(() => processor.process(root), XIncludeSecurityError);
  assert.equal(resolver.callCount, 0);
});
