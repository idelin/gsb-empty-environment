# 窗口消息发送者导航竞争

页面验证postMessage来源后，发送者窗口可能已导航到另一文档。为TypeScript宿主实现消息接收组件，将发送时的来源与文档代次作为不可变可信元数据，响应能力只对该文档有效；窗口对象复用不能继承能力。用可控导航事件验证接收、验证、回复交错时不向新文档泄漏内容。

## 设计

- `src/platform.ts`：模拟平台。`SimulatedWindow` 相当于 WindowProxy——宿主持有的引用在导航后保持稳定，但内部文档被替换，每个文档拥有不可伪造的 `generation` 令牌。`postMessage` 在**发送时**把 `{ origin, generation, document }` 冻结为可信元数据，存入 `PlatformMessageEvent` 的私有字段；页面代码可读但无法伪造或篡改。消息投递走显式可控的事件循环（`runNextTask` / `flush`），便于精确交错导航时序。
- `src/secure-receiver.ts`：宿主接收组件 `SecureMessageReceiver`。校验可信元数据中的来源是否在白名单内；通过后签发 `ReplyCapability`，绑定发送时的来源与文档代次。回复时平台在入队与投递两个时点都复核目标窗口当前代次，窗口一旦导航（同源或跨源），能力即失效，窗口对象复用不会继承能力，新文档永远收不到旧文档的回复。

## 运行测试

```sh
npm test   # node --experimental-strip-types --test（需要 Node >= 22.6）
```

`test/navigation-race.test.ts` 用可控导航事件覆盖：同代次正常回复、同源/跨源导航后回复被丢弃、接收-验证-回复与导航交错、窗口复用不继承能力、投递前导航的 TOCTOU 复核、非法来源与伪造事件拒绝、元数据不可变性，以及一个"只钉住 origin 的朴素实现确实会泄漏"的对照用例。
