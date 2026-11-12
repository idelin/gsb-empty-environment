# 跨文档广播通道分区

浏览器测试宿主使用的可导入 TypeScript 广播通道路由包。频道身份同时绑定
**存储分区（来源 + 顶层站点）** 与 **文档生命周期**，在进程内模拟第三方存储分区
下的 `BroadcastChannel` 语义。

## 核心语义

- **双键分区**：分区键为 `来源(scheme://host)` 与 `顶层站点(eTLD+1)` 的组合；
  同来源嵌套在不同顶层站点下、或同站点内不同来源的同名频道互不可见。
- **文档生命周期绑定**：每个文档持有单调递增的 `generation` 令牌，频道创建时
  归属该文档；导航或关闭框架会同步关闭其全部频道并从路由器摘除，旧订阅立即失效。
- **文档号复用安全**：相同 `docId` 重新打开得到新一代令牌，旧频道无法收到新流量，
  同一时刻存在两个相同 `docId` 的活文档会直接报错。
- **排队保留发送时分区**：`postMessage` 入队时把分区键快照写入信封，发送文档
  随后导航到其他分区，消息仍按入队时的分区路由。
- **确定性一次交付**：每条信封在微任务排空时仅出队一次，接收者集合在出队点快照；
  排队期间关闭的频道不会被投递，监听器内部关闭频道只影响后续信封，当前信封对
  已快照的监听器恰好投递一次；并发 `flush()` 不会重复投递。
- **发送方不接收自己的消息**，与浏览器 `BroadcastChannel` 行为一致。

## 模块结构

- `src/partition.ts`：来源/站点解析与存储分区键。
- `src/router.ts`：FIFO 队列、微任务排空、接收者快照与投递记录。
- `src/channel.ts`：`PartitionedBroadcastChannel`，分区绑定的频道实现。
- `src/document.ts`：文档注册表与代际令牌。
- `src/host.ts`：`TestBrowserHost` 测试宿主（开框架、导航、关闭、排空）。
- `index.ts`：统一导出入口。

## 使用示例

```ts
import { TestBrowserHost } from "./index.ts";

const host = new TestBrowserHost();
const top = host.openFrame("doc-top", "https://a.example");
const embed = host.openFrame("doc-embed", "https://ads.example/widget", "https://a.example");

embed.createChannel<string>("sync").addListener((message) => {
  console.log(message.data, message.partitionKey);
});
top.createChannel<string>("sync").postMessage("hello");
await host.flush();

const next = host.navigate(embed, { topLevelSite: "https://b.example" });
```

## 测试

```sh
node --experimental-strip-types --test "test/**/*.test.ts"
```

测试覆盖嵌套站点第三方分区矩阵、同名频道隔离、文档号复用代际隔离、导航失效、
排队分区快照，以及关闭与投递并发下的确定性一次交付（共 18 个用例）。
