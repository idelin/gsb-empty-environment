# Delta-CRDT 增量压缩同步协议

将全量 State-based CRDT 优化为增量 Delta 包传输：各节点为每个远端对等端
维护一条**已确认水位**（版本向量），仅生成并发送自上次确认之后发生变动的
Delta 差异切片；无变动时不产生任何网络流量。

## 架构

```
src/
  version-vector.ts   版本向量（合并 / 逐副本取最小值）
  document-crdt.ts    RGA 风格 Delta-CRDT 文档（字符即节点，删除为墓碑）
  sync-node.ts        同步节点：水位跟踪、Delta 生成、Ack 处理、日志回收
examples/demo.ts      10000 节点文档改 1 字符的端到端演示
test/sync.test.ts     node:test 验收测试
```

## 协议流程

```
A                                    B
 |--- delta{base: 水位, ops: 切片} -->|  仅含自上次确认后的变动
 |<-- ack{vv: B 当前版本向量} --------|  B 应用后回传确认水位
 |  (A 推进水位；全部被确认的前缀回收)  |
```

- `SyncNode.deltaPacketFor(peer)`：自该对等端确认水位以来的操作切片，
  无变动返回 `null`。
- `SyncNode.receive(packet)`：应用 Delta/全量后回 Ack；收到 Ack 推进水位
  并回收所有对等端均已确认的日志前缀。
- 乱序到达的操作按副本缓冲、缺口补齐后按序应用；重复投递幂等。

## 运行

要求 Node.js >= 23.6（原生 TypeScript 类型擦除，零依赖）。

```bash
npm test    # node --test 'test/*.test.ts'
npm run demo
```

## 验收结果

**测试（8/8 通过）**：

```
✔ 验收：10000 节点文档修改 1 字符，Delta 包为常数级字节
✔ 验收：接收端应用 Delta 后与全量传输完全等价
✔ 确认水位：仅生成并发送自上次确认后的变动
✔ 并发编辑双向同步后收敛
✔ Delta 乱序与重复投递仍收敛（幂等）
✔ 删除语义通过 Delta 正确传播（墓碑）
✔ 所有对等端确认后回收增量日志
✔ 新节点未引导时自动回退全量状态包
```

**演示输出（10000 节点文档，修改 1 字符）**：

```
[引导] 全量状态包大小: 846,743 字节
[增量] Delta 包大小: 127 字节（常数级，与文档规模无关）
[压缩] 6667x（全量 846,743B -> 增量 127B）
[内容] {"kind":"delta","from":"A","to":"B","base":{"A":10000},
        "ops":[{"t":"i","replica":"A","seq":10001,"after":"A:5000","char":"Z"}]}
[校验] B 应用 Delta 后与 A 完全等价: true
[水位] A 记录的 B 端确认水位: {"A":10001}
[去重] 确认后再次生成 Delta: null（无变动不发送）
[对照] C 经全量传输后与 A 完全等价: true
```
