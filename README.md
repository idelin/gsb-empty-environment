# font-measure-cache

带版本失效与可控调度的字体测量缓存。纯 TypeScript，零第三方依赖，
核心计算（字形簇成形、宽度求和、布局反馈）完全由夹具数据驱动，不借浏览器布局引擎。

## 运行要求

- Node.js >= 23.6（原生运行 TypeScript）；或 >= 22.6（自动带类型擦除标志）。
- 仓库内 `.nvmrc` 指定 23.9.0。

```sh
npm test    # node --test test/*.test.ts（Node 内置测试运行器）
npm run demo # node examples/demo.ts
```

## 结构

- `src/types.ts` — 字体/样式/字形簇/测量结果/节点树类型。
- `src/fixtures.ts` — 夹具：serif v1/v2、mono v1 字形表与节点树。
- `src/cache.ts` — `ByteCache`：分区配额 + 分区间借用，额度按条目实际大小计，
  总额守恒；按字体精确失效；写入代际下限拒绝旧回包。
- `src/fontRegistry.ts` — 不可变快照，热更新单次引用替换完成原子代际切换；
  晚到的异步加载校验版本，旧版本不得覆盖。
- `src/scheduler.ts` — 可控调度器：同键合并、订阅者引用计数
  （取消一个订阅者不取消其他等待者）、共享依赖、手动 `pump/flush/complete`
  注入旧回包与乱序回包，回包按代际校验，过期丢弃并级联。
- `src/measure.ts` — 测量引擎：预估同步发布、最终异步发布；
  字体度量/字形簇/样式组合三层缓存，键含字体版本与字距；
  stale 后按新代际重发（上限 8 次）。
- `src/layout.ts` — 布局引擎：最终测量驱动反馈迭代，`maxPasses` 硬性上限，
  反馈产生的新请求不会形成无界循环。

## 缓存键

- `font:<fontId>@<version>`
- `cluster:<fontId>@<version>:<text>`
- `measure:<fontId>@<version>:<fontSize>:<letterSpacing>:<text>`

相同文本不同字距落在不同键上，不会共享错误结果。
