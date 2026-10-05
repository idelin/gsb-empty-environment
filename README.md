# 链路状态同步模拟器

用 TypeScript 实现的链路状态协议（LSP/LSA 风格）同步模拟器，演示泛洪、摘要交换与缺失补齐，以及分区后重连的全网收敛校验。

## 运行

需要 Node.js ≥ 23.6（原生类型擦除运行 TS），或 Node ≥ 22.6 加 `--experimental-strip-types`：

```sh
node src/main.ts        # Node >= 23.6
npm run sim
```

## 核心设计

- **LSA 新鲜度 = (代次 generation, 版本 version)**：按字典序比较。收到不比本地新的同源更新直接跳过（`SimNode.install`，`src/node.ts:97`），因此被新版本覆盖的旧更新可以安全丢弃，无需按序送达。
- **删除后重建必须用新代次**：`withdraw()` 产生同代次的墓碑 LSA（`withdrawn: true`），墓碑随泛洪/摘要同步传播；`recreate()` 强制 `generation + 1`、`version` 重置为 1。旧代次的任何迟到更新都无法覆盖新代次（见场景2）。`updateLinks()` 在已撤回状态下会抛错，从 API 上禁止"同代次复活"。
- **每来源进度向量，而非全局最大版本**：摘要是 `Map<origin, (generation, version, digest)>` 的完整枚举（`sendSummaryTo` / `onSummary`，`src/node.ts:79`）。场景3 证明：两个节点全局最大版本相同（都是 5）但数据库不同，全局水位线会误判为已同步，而逐来源摘要能发现彼此缺失。
- **摘要交换 + 缺失补齐**：链路 up 时两端互发摘要；收摘要方对"对方更新/自己缺失"的条目发 `request`，对"自己更新/对方缺失"的条目直接推 `update`；收 `request` 方回 `update`。补齐的 LSA 走正常 install 路径，会继续向其他邻居泛洪，因此增量同步能传遍全网。
- **拓扑一致性**：`canonicalTopology`（`src/topology.ts`）只采纳双向确认（双方 LSA 互相包含对方）且未撤回的邻接；收敛校验比较所有节点的 LSDB 摘要与拓扑序列化结果。

## 场景（src/main.ts）

1. **过期跳过**：乱序投递下旧版本被跳过，新版本保留。
2. **删除重建**：gen1 的迟到更新无法覆盖 gen2 重建。
3. **版本向量**：全局最大版本相同但数据库不同，摘要交换后互相补全。
4. **分区重连**：8 节点切分为两个分区独立演化（含多次更新、n5 删除后以 gen2 重建、n6 删除保留墓碑），重连后经摘要交换收敛，校验 8 个节点 LSDB 与拓扑完全一致。

## 文件

- `src/lsa.ts` — LSA/摘要结构、新鲜度比较、规范化序列化与 FNV-1a 摘要
- `src/node.ts` — 节点：发源/更新/撤回/重建、泛洪、摘要/请求/更新处理
- `src/network.ts` — 离散事件网络：链路、分区切换、随机时延、确定性随机种子
- `src/topology.ts` — 由 LSDB 推导双向确认邻接与规范化拓扑
- `src/main.ts` — 四个验证场景与断言
