import { test } from "node:test";
import assert from "node:assert/strict";
import { TopicsHost, type BrowsingContext } from "./index.ts";

/** 固定纪元种子：第 N 个纪元用 fill(N+1) 的 32 字节，保证测试可复现。 */
function epochSeed(n: number): Uint8Array {
  return new Uint8Array(32).fill(n + 1);
}

function context(overrides: Partial<BrowsingContext> = {}): BrowsingContext {
  return {
    topLevelSite: "news.example",
    caller: "ad.example",
    inIframe: false,
    isCrossOrigin: false,
    hasBrowsingTopicsPolicy: false,
    ...overrides,
  };
}

/** 测试内独立重算 HMAC 派生，锁定宿主的确定性随机算法。 */
async function expectedDraw(
  seed: Uint8Array,
  epoch: number,
  topLevelSite: string,
  caller: string,
): Promise<{ keepRoll: number; topicIndex: bigint }> {
  const key = await crypto.subtle.importKey(
    "raw",
    seed,
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const mac = new Uint8Array(
    await crypto.subtle.sign(
      "HMAC",
      key,
      new TextEncoder().encode(`topics|${epoch}|${topLevelSite}|${caller}`),
    ),
  );
  const read = (offset: number) => {
    let value = 0n;
    for (let i = 0; i < 8; i++) {
      value = (value << 8n) | BigInt(mac[offset + i]);
    }
    return value;
  };
  return {
    topicIndex: read(0),
    keepRoll: Number(read(8)) / 2 ** 64,
  };
}

test("从未观察过的话题不会泄露给新嵌入的调用方", async () => {
  const host = new TopicsHost();
  await host.startEpoch(epochSeed(0));
  host.observe(1, "incumbent.example");
  host.observe(2, "incumbent.example");
  host.observe(2, "ad.example");

  const lateCaller = await host.browsingTopicsWithEpoch(
    context({ caller: "tracker-new.example" }),
  );
  assert.deepEqual(lateCaller, []);

  const incumbent = await host.browsingTopicsWithEpoch(
    context({ caller: "incumbent.example" }),
  );
  assert.equal(incumbent.length, 1);
  assert.ok([1, 2].includes(incumbent[0].topic));

  const partial = await host.browsingTopicsWithEpoch(context({ caller: "ad.example" }));
  assert.deepEqual(partial.map((entry) => entry.topic), [2]);
});

test("重复调用结果完全相同，且只含调用方观察过的话题", async () => {
  const host = new TopicsHost();
  await host.startEpoch(epochSeed(0));
  for (const topic of [3, 7, 11, 23]) {
    host.observe(topic, "ad.example");
  }
  host.observe(99, "other.example");

  const ctx = context();
  const first = await host.browsingTopics(ctx);
  for (let i = 0; i < 5; i++) {
    assert.deepEqual(await host.browsingTopics(ctx), first);
  }
  assert.equal(first.length, 1);
  assert.ok([3, 7, 11, 23].includes(first[0].topic));

  // 与独立重算的 HMAC 派生结果一致，锁定确定性算法。
  const draw = await expectedDraw(epochSeed(0), 0, "news.example", "ad.example");
  const observed = [3, 7, 11, 23];
  assert.equal(first[0].topic, observed[Number(draw.topicIndex % BigInt(observed.length))]);

  // 换顶层站点是另一个组合，可以选中不同话题，但各自重复调用仍稳定。
  const otherSite = await host.browsingTopics(context({ topLevelSite: "shop.example" }));
  assert.deepEqual(
    await host.browsingTopics(context({ topLevelSite: "shop.example" })),
    otherSite,
  );
});

test("跨纪元：每纪元各出一个话题、最多 3 个、种子独立不可关联", async () => {
  const host = new TopicsHost();
  for (let epoch = 0; epoch < 4; epoch++) {
    await host.startEpoch(epochSeed(epoch));
    host.observe(100 + epoch, "ad.example");
    host.observe(500 + epoch, "other.example");
  }

  const picked = await host.browsingTopicsWithEpoch(context());
  assert.equal(picked.length, 3);
  assert.deepEqual(
    picked.map((entry) => entry.epoch),
    [1, 2, 3],
  );
  assert.deepEqual(
    picked.map((entry) => entry.topic),
    [101, 102, 103],
  );

  // 每个纪元的选择都能由该纪元自己的独立种子解释。
  for (const entry of picked) {
    const draw = await expectedDraw(
      epochSeed(entry.epoch),
      entry.epoch,
      "news.example",
      "ad.example",
    );
    assert.equal(entry.topic, 100 + entry.epoch);
    assert.ok(draw.keepRoll >= 0 && draw.keepRoll < 1);
  }

  // 独立种子 => 同调用方在不同纪元的 HMAC 输出不同，无法关联随机源。
  const draws = await Promise.all(
    [1, 2, 3].map((epoch) =>
      expectedDraw(epochSeed(epoch), epoch, "news.example", "ad.example"),
    ),
  );
  const rolls = new Set(draws.map((draw) => draw.keepRoll));
  const indices = new Set(draws.map((draw) => draw.topicIndex));
  assert.equal(rolls.size, 3);
  assert.equal(indices.size, 3);

  // 某纪元未观察的调用方拿不到该纪元的话题，其余纪元照常。
  const host2 = new TopicsHost();
  await host2.startEpoch(epochSeed(0));
  host2.observe(1, "ad.example");
  await host2.startEpoch(epochSeed(1));
  // 纪元 1 中 ad.example 没有任何观察
  host2.observe(2, "other.example");
  await host2.startEpoch(epochSeed(2));
  host2.observe(3, "ad.example");
  const scoped = await host2.browsingTopicsWithEpoch(context());
  assert.deepEqual(
    scoped.map((entry) => [entry.epoch, entry.topic]),
    [
      [0, 1],
      [2, 3],
    ],
  );
});

test("filterRate 的取舍由种子确定且重复调用稳定", async () => {
  const host = new TopicsHost({ filterRate: 1 });
  await host.startEpoch(epochSeed(0));
  host.observe(1, "ad.example");
  assert.deepEqual(await host.browsingTopics(context()), []);

  // 找到一个 keepRoll 高于 0.5 的组合，验证阈值边界行为确定。
  const probing = new TopicsHost({ filterRate: 0.5 });
  await probing.startEpoch(epochSeed(0));
  probing.observe(1, "ad.example");
  const draw = await expectedDraw(epochSeed(0), 0, "news.example", "ad.example");
  const result = await probing.browsingTopics(context());
  if (draw.keepRoll < 0.5) {
    assert.deepEqual(result, []);
  } else {
    assert.deepEqual(result.map((entry) => entry.topic), [1]);
  }
  assert.deepEqual(await probing.browsingTopics(context()), result);
});

test("跨源 iframe 无 browsing-topics 权限策略时返回空数组", async () => {
  const host = new TopicsHost();
  await host.startEpoch(epochSeed(0));
  host.observe(1, "ad.example");

  const blocked = context({
    inIframe: true,
    isCrossOrigin: true,
    hasBrowsingTopicsPolicy: false,
  });
  assert.deepEqual(await host.browsingTopics(blocked), []);
  assert.deepEqual(await host.browsingTopicsWithEpoch(blocked), []);

  // 授予权限策略后正常返回。
  const allowed = context({
    inIframe: true,
    isCrossOrigin: true,
    hasBrowsingTopicsPolicy: true,
  });
  assert.equal((await host.browsingTopics(allowed)).length, 1);

  // 同源 iframe 与顶层文档不需要权限策略。
  const sameOriginFrame = context({ inIframe: true, isCrossOrigin: false });
  assert.equal((await host.browsingTopics(sameOriginFrame)).length, 1);
  const topDoc = context({ inIframe: false, isCrossOrigin: false });
  assert.equal((await host.browsingTopics(topDoc)).length, 1);
});

test("HMAC 锁定向量：派生算法变化会显式失败", async () => {
  const draw = await expectedDraw(epochSeed(0), 0, "news.example", "ad.example");
  assert.equal(
    draw.topicIndex,
    579785901418085721n,
  );
  assert.ok(Math.abs(draw.keepRoll - 0.5849380550889473) < Number.EPSILON);
});

test("公共 browsingTopics() 返回浏览器 API 形状且不含内部纪元字段", async () => {
  const host = new TopicsHost({ taxonomyVersion: "chrome.2", modelVersion: "7" });
  await host.startEpoch(epochSeed(0));
  host.observe(42, "ad.example");

  const result = await host.browsingTopics(context());
  assert.deepEqual(result, [
    {
      topic: 42,
      version: "chrome.2",
      configVersion: "chrome.2",
      modelVersion: "7",
      taxonomyVersion: "chrome.2",
    },
  ]);
  assert.equal("epoch" in result[0], false);
});
