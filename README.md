# Topics 观察者过滤与纪元隔离

Topics API 浏览器宿主侧的 TypeScript 实现，供 `document.browsingTopics()` 调用，零依赖，可直接用 Node 内置测试运行器验证。

## 文件结构

- `src/topics-host.ts` — `TopicsHost`：每周纪元存储 `(话题 -> 观察过该话题的调用方集合)`，并实现话题返回算法
- `src/rng.ts` — 由宿主纪元种子派生确定性 PRNG（HMAC-SHA256 + mulberry32）
- `src/document.ts` — `document.browsingTopics()` 适配层与 `browsing-topics` 权限策略判定
- `test/topics.test.ts` — 13 个用例，覆盖全部要求场景
- `index.ts` — 包入口

## 安全模型如何对应需求

- **从未观察的话题不泄露**：每个纪元保存 `Map<话题, Set<调用方 origin>>`，返回前按调用方 origin 过滤；随机噪声话题也只从该调用方自己观察过的集合中抽取，新嵌入的第三方脚本拿不到任何话题。
- **最近 3 个纪元、每纪元一个、最多 3 个**：只读取最近 3 个已关闭纪元（进行中的纪元不返回），`pruneClosedEpochs()` 同步淘汰更老的纪元。
- **重复调用结果相同**：每个 `(顶层站点, 调用方, 纪元)` 组合用 `HMAC(纪元种子, 顶层站点 || 调用方)` 派生独立 PRNG，过滤/随机取舍全部由该确定性流决定；同一组合重复调用、甚至换宿主实例重建，结果一致，无法靠反复调用剥离噪声。
- **纪元间不可关联**：每个纪元通过 `crypto.getRandomValues` 生成独立种子（测试可注入固定种子），跨纪元的派生流互不相关；测试验证更换第二纪元种子只影响第二纪元的结果。
- **无权限跨源 iframe 返回空数组**：`resolveTopicsPolicy()` 对跨源 iframe 要求显式 `allow="browsing-topics"`，否则 `fetchTopics` 在入口直接返回 `[]`；同源 iframe 与顶层文档沿用默认允许。

## 用法

```ts
import { TopicsHost, installBrowsingTopics } from './index.ts';

const host = new TopicsHost();

// 浏览器在带广告的页面上记录观察（当前纪元内）
host.recordObservation({
  topLevelSite: 'https://news.example',
  callerOrigin: 'https://ads.example',
  topic: 42,
});

// 每周滚动纪元（也可由定时器调用 rollEpochsByTime()）
host.advanceEpoch();

// 安装到页面 document 上
installBrowsingTopics(document, host, frameContext, 'https://ads.example');
const topics = await document.browsingTopics();
```

## 测试

```sh
node --experimental-strip-types --test
```

覆盖：从未观察返回空、重复调用幂等、跨纪元种子独立与 3 纪元上限、无权限跨源 iframe 返回空数组，以及顶层站点键控、过滤概率确定性、同源 iframe 等边界。
