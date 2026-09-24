# stable-id-text-sequence

离线优先的文本序列 CRDT（TypeScript，零依赖，Node.js 内置测试）。

## 特性

- **稳定元素 ID**：每个元素由 `(replica, counter)` 唯一标识，永不复用。
- **因果缓冲**：插入携带父元素引用；依赖未到的操作进入被缓冲，**不会**被挂到头部，依赖到达后自动归位。
- **幂等删除 + 墓碑**：删除即打墓碑标记，重复删除无效果；删除先于插入到达时会被记住，插入落地后字符仍不可见。
- **合并性质**：`merge` 是集合并 + 墓碑 OR，满足交换律、结合律、幂等（有测试为证）。
- **水位 GC**：`gc(acks)` 只在**所有**副本的水位都覆盖某元素时才回收它，且只回收叶子墓碑（不回收仍有子元素的墓碑）。
- **UTF-16 公开偏移**：`insertAt` / `deleteAt` / `length` 均以 UTF-16 码元计；任何会切开代理对的偏移直接抛 `RangeError`。LF 与 CRLF 均按码元计数（CRLF = 2）。

## 运行

```sh
npm test    # node --test（Node >= 23，原生 type stripping）
npm run demo
```

## API 速览

```ts
const doc = new TextDoc('A');
const insertOps = doc.insertAt(0, 'hi');   // 本地生效并返回待广播的 Op[]
const deleteOps = doc.deleteAt(0, 1);
remote.apply(op);                          // 任意顺序、可重复投递
doc.merge(other);                          // 或 TextDoc.merged(a, b)
doc.gc(acks);                              // acks: Map<replica, Map<replica, counter>>
doc.getText(); doc.length;                 // UTF-16 视角
```

## 设计要点

- 元素值为单个 Unicode 码位（1–2 个 UTF-16 码元），因此公开偏移边界天然不会切开代理对。
- 同父兄弟按元素 ID 全序降序排列（RGA 风格），新插入落在父元素紧邻之后；全序保证各副本确定性一致。
- 水位取每个副本已**集成**（非缓冲）操作的连续前缀，保守可 ack。
- GC 假设：`acks` 的键声明了全部副本成员（未确认的副本传空 Map）。
