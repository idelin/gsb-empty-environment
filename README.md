# 文件输入选择票据

网页只能读取用户明确选择的文件，控件路径字符串不能成为文件能力。实现可导入的TypeScript选择会话包，可信选择事件生成绑定文档与对象ID的只读票据，脚本只能持票读流；重新选择替换集合，旧异步回调不得获得新文件。用多文件选择、取消和文档导航测试票据归属与读流结束。

## 能力模型

- **路径不是能力**：包内没有任何接受路径字符串或对象 ID 的读取入口，`objectId` 只作为内部存储键，脚本拿到它也无法读字节。
- **票据是唯一读取能力**：可信选择事件为每个被选文件签发一张 `ReadonlyFileTicket`，令牌为 256 位随机值（`ft_` + 64 个十六进制字符），猜测不可行。
- **双重绑定**：每张票据绑定 `documentId` 与 `objectId`；跨文档会话出示票据会被拒绝。
- **一次性读流**：`openReadStream` 兑现货票后票据即 `consumed`，重复使用抛 `TicketConsumedError`。
- **可信事件**：`TrustedFilePicker` 生成带内部 symbol 品牌的事件（模拟 DOM `isTrusted`），脚本手工构造的事件抛 `UntrustedEventError`，不会签发任何票据。
- **重新选择即替换**：新选择先签发新集合，再吊销旧集合全部票据并中止其在读流；旧异步回调只持有失效票据，永远接触不到新文件。
- **取消保持现状**：取消选择器只发 `cancel` 事件，不签发、不吊销票据。
- **文档导航/销毁**：`navigate` 与 `dispose` 吊销该文档全部票据，进行中的读流以 `TicketRevokedError` 结束。

## 结构

- `src/types.ts` — 票据、事件等公开类型
- `src/errors.ts` — 票据错误类型（含显式拒绝路径查询的 `PathAccessError`）
- `src/registry.ts` — 票据权威注册表：签发、兑现、吊销、流中止
- `src/trustedEvents.ts` — 不可伪造的可信事件品牌
- `src/picker.ts` — 可信选择器主机（确认/取消）
- `src/session.ts` — `SelectionSession`：事件处理与唯一的读流入口
- `src/storage.ts` — 可信侧文件存储抽象与内存实现
- `test/selection-session.test.ts` — 多文件选择、取消、重选替换、旧回调隔离、导航中止、跨文档归属等 11 个用例

## 用法

```ts
import {
  InMemoryFileStorage,
  SelectionSession,
  TrustedFilePicker,
  createFileObject,
} from './index.ts';

const storage = new InMemoryFileStorage();
const file = createFileObject(
  { objectId: 'obj-1', name: 'a.txt', size: 5, lastModified: Date.now() },
  new TextEncoder().encode('hello'),
);
storage.add(file);

const session = new SelectionSession('doc-1', storage);
const picker = new TrustedFilePicker();

session.addEventListener('selection', async (event) => {
  for (const ticket of event.tickets) {
    const stream = session.openReadStream(ticket); // 持票读流
    // ...
  }
});

picker.confirmSelection(session, [file]); // 用户明确选择
```

## 运行测试

需要 Node.js >= 22.6（依赖 `--experimental-strip-types` 直接运行 TypeScript）：

```sh
npm test
```

类型检查（需本地安装 TypeScript）：

```sh
npm run typecheck
```
