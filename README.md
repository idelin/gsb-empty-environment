# WebMIDI 系统独占发送过滤

TypeScript 浏览器宿主可直接从 `index.ts` 导入：

- `MidiByteStreamFilter`：有状态的 MIDI 字节流解析器，跨多次写入保留 SysEx 状态。
- `SysexPortRegistry`：按稳定的 `MIDIOutput.id` 记录文档独占租约。
- `WebMidiSendFilter` / `createWebMidiSendFilter`：包装 `MIDIOutput.send`，在授权范围内过滤并调用原始端口。
- `MidiDocumentSession` / `createMidiDocumentSession`：聚合一个文档的过滤器，并在 `pagehide` 或 `unload` 时释放全部端口租约。

## 未授权 SysEx

```ts
import { createWebMidiSendFilter } from "./index.ts";

const filter = createWebMidiSendFilter(output, {
  sysexEnabled: false,
  documentId: "tab-1",
});

filter.send([0x90, 0x3c, 0x40]); // 正常音符发送
filter.send([0xf0, 0x7d, 0x01]); // 被截断，SysEx 被移除
filter.send([0x02, 0xf7]);       // 仍在前一条 SysEx 内，全部移除
```

未授权过滤器会拒绝 `F0`、SysEx 载荷和 `F7`，并跨 `send` 调用保持状态。`F8` 到 `FF` 的实时字节默认透传，且不会终止 SysEx；需要连同实时字节一起丢弃时设置 `passRealtimeDuringSysex: false`。运行状态字节同样不会破坏拆分写入的识别。

## 独占授权

```ts
import {
  MidiDocumentSession,
  SysexPortRegistry,
} from "./index.ts";

const registry = new SysexPortRegistry();
const sessionA = new MidiDocumentSession({
  documentId: "tab-a",
  registry,
});
const sessionB = new MidiDocumentSession({
  documentId: "tab-b",
  registry,
});

const a = sessionA.createFilter(outputA, { sysexEnabled: true });
const b = sessionB.createFilter(outputB, { sysexEnabled: true });

a.send([0xf0, 0x7d, 0x01, 0xf7]); // 成功并取得端口独占租约
b.send([0x90, 0x3c, 0x40]);       // 抛出 MidiSendRejectedError
```

同一个 `portId`（默认取 `output.id`）在租约存续期间只允许所有文档中的唯一所有者发送，其他文档即使只发普通 MIDI 也会被拒绝。未授权文档可以共享未被独占的端口发送普通 MIDI，但其 SysEx 始终被过滤。

`MidiDocumentSession` 默认监听 `pagehide` 和 `unload`；页面导航后自动关闭会话并释放端口，此时另一个文档可重新取得租约。也可显式调用 `session.close()` 或 `filter.release()`。在非浏览器测试环境中可传 `releaseOnPageHide: false`。

## 宿主约束

`registry` 必须由宿主控制面创建并在需要互斥的文档之间共享，不能依赖各页面私有的默认注册表。客户端过滤器是纵深防御；若页面本身不可信，宿主还必须隔离原始 `MIDIOutput`，并在未授权时使用 `navigator.requestMIDIAccess({ sysex: false })`。

运行测试：

```sh
npm test
```
