# 增量集合差异证明（Incremental Set Difference Proof）

两端各维护 **键 → (版本, 值)** 的映射及**删除墓碑（tombstone）**，
通过**分层哈希摘要树**按子树定位差异；摘要只用于剪枝，
协议同时提供**显式精确核验模式**与**可复核差异列表**。
仅依赖 Python 3.11 标准库，所有示例数据均为合成数据。

## 运行

```bash
python3 -m unittest discover -s tests -v     # 运行测试
python3 -m setdiff demo --hash constant      # 合成数据演示
python3 -m setdiff reconcile --local A.json --remote B.json \
    --mode exact --hash constant --check     # 差异对账
python3 -m setdiff merge --local A.json --remote B.json
```

## 数据模型与存储文件格式

存储文件为 JSON，键任意字符串，`version` 为非负整数，
`deleted=true` 或值缺省表示删除墓碑：

```json
{
  "records": [
    {"key": "user:1", "version": 1, "deleted": false,
     "value": "alice-v1", "value_b64": "YWxpY2UtdjE="},
    {"key": "user:4", "version": 6, "deleted": true,
     "value": null, "value_b64": null}
  ]
}
```

- `value`：UTF-8 可读形式；`value_b64`：值的 Base64，**解析时优先**，
  因此任意二进制字节可无损往返（见 `setdiff/model.py`）。
- 墓碑不是立即丢弃的键：它携带版本号参与合并与对账，
  墓碑版本高于存活值版本时，合并结果为**删除**（LWW，见下）。

## 分层摘要树

`setdiff/tree.py`：固定深度的 16 叉（十六进制字符）前缀树，深度可配（默认 2，
共 256 个叶节点）。

- **路由**：键经固定 SHA-256 取前 `depth` 个十六进制字符落入叶节点；
  路由哈希与可注入的内容哈希器解耦，恒定哈希下同一键仍路由到同一叶。
- **叶摘要** = `H("leaf:" + 各记录指纹拼接)`，记录指纹含
  键 / 版本 / 删除标志 / 值；空叶有确定性空摘要。
- **内部摘要** = `H("node:" + 16 个子摘要拼接)`。
- 记录顺序不影响摘要（按键规范化排序）。

## 概率模式与确定模式（核心差别）

| 维度 | `--mode probabilistic`（概率） | `--mode exact`（确定） |
| --- | --- | --- |
| 策略 | 逐层比较摘要，只下钻摘要不同的子树，最终只拉取**分歧叶**的完整记录逐条比较 | 不依据摘要剪枝，拉取对端**全部记录**逐条精确比较 |
| “相等”结论 | **概率性**：哈希无碰撞时成立；SHA-256 下碰撞概率可忽略，但永非零 | **确定性**：逐条核验后成立（`exact_verdict=true`） |
| 传输量 | 少：`records_sent` 只含分歧叶条目 | 大：`records_sent` = 对端全量条目 |
| 恒定哈希测试 | 摘要恒等 → 可能**漏报**（`equal=true`），但协议**显式发出碰撞预警** | 不受影响，仍能发现全部差异 |

**协议立场**：任何模式都不会仅凭“摘要相等”就宣称集合相同——
概率模式的 `equal` 仅为概率性，`exact_verdict` 只有精确模式且相等时才为真；
确定结论只能由 `exact` 模式给出。`Sha256Hasher` 退化探测（
`looks_degenerate`）发现哈希器对已知不同输入输出相同摘要时会附预警。

## 可复核差异列表

每条差异（`DiffEntry`）含 `kind` 与两侧完整记录视图：

- `only_remote` / `only_local`：键只在一侧存在；
- `value_mismatch`：键与版本相同但值不同（并发写入，需平局裁决）；
- `version_mismatch`：同存/同删状态不同版本；
- `tombstone_version_mismatch`：一侧墓碑、一侧存活值且版本不同。

对账报告 JSON 字段：`equal`、`exact_verdict`、`mode`、`hash`、
`depth`、`root_local/root_remote`、`diff_count`、`diffs`、`stats`、`warnings`。

## 统计口径（Stats，本地 ← 对端方向）

- `digest_requests`：摘要请求消息数（按层批处理，1 层 1 条）；
- `records_requests`：记录请求消息数（分歧叶批量 1 条；精确模式 1 条全量请求）；
- `digest_nodes_sent`：对端回传的摘要节点数；
- `records_sent`：对端回传的**完整记录条目数（含墓碑）**；
- `bytes_sent`：响应负载规范化 JSON 编码字节数之和（估算，用于比较模式开销）。

例：合成数据在 SHA-256、深度 2 下，概率模式仅回传分歧叶的 3 条记录即定位全部
4 处差异；恒定哈希下回传 0 条并漏报（附预警）；精确模式回传 4 条、确定检出。

## 合并规则（LWW，确定性可收敛）

`VersionedStore.merge`（`setdiff/store.py`）：

1. 版本高者胜出——**墓碑版本高于存活值时合并为删除**；
2. 版本相同，墓碑优先于存活值；
3. 版本相同且同为存活值，值字节大者胜出。

该全序规则保证两个节点以任意顺序合并最终收敛到同一状态。

## 增量语义

增量对账由“上一轮已知的摘要/状态”驱动：两端只交换摘要不同的子树与对应记录，
相同子树被剪枝跳过（概率模式）；调用方可在版本推进后反复执行 `reconcile`。
精确模式不剪枝，是对概率模式结论的显式核验手段。

## 目录

- `setdiff/model.py`：记录模型与 JSON 编解码
- `setdiff/store.py`：版本化存储、墓碑、LWW 合并与合并报告
- `setdiff/hasher.py`：可注入哈希器（`Sha256Hasher` / `ConstantHasher`）
- `setdiff/tree.py`：分层摘要树与退化哈希探测
- `setdiff/protocol.py`：两种对账模式、差异分类、传输统计
- `setdiff/cli.py` / `__main__.py`：命令行
- `tests/test_setdiff.py`：26 个 unittest 用例
- `examples/`：合成示例存储 A/B
