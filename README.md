# chunked-file-verifier

按块哈希树的文件校验工具（TypeScript，Bun 运行）。证明方对文件建立
Merkle 承诺；验证方只拿到**部分块 + 对应路径证明**即可按请求范围完成校验，
无需读取完整文件。

## 哈希树规则（v1，固定）

- 文件按 `chunkSize` 分块，块数 `ceil(length / chunkSize)`。
- **末块填充规则固定**：末块在哈希前以零字节补齐到 `chunkSize`。
- 叶子 `H(0x00 || u64le(index) || paddedChunk)`；内部节点 `H(0x01 || left || right)`。
- 奇数节点**提升**（不复制）到上一层，因此规范证明的路径长度唯一，
  含重复路径节点的证明直接判为非法证据。
- 空文件树根为 `H(0x02)`。
- **承诺绑定长度与块大小**：
  `commitment = H("MRV1" || u64le(fileLength) || u32le(chunkSize) || treeRoot)`。

## 验证语义

`verifyRange` 对范围 `[start, end)` 内每个块给出判定：

| 判定 | 含义 |
| --- | --- |
| `ok` | 数据 + 规范证明可重算到受信承诺 |
| `bad` | 数据与证据齐备且格式合法，但与承诺矛盾（损坏/伪造） |
| `missing` | 块未收到 —— **不视为损坏** |
| `insufficient` | 有数据但证明缺失/不规范，或承诺头本身不一致 |

## 使用

```bash
bun test                                  # 运行全部测试
bun src/cli.ts commit file.bin            # 打印承诺 JSON（长度+块大小+树根）
bun src/cli.ts prove  file.bin --range 100:200 > bundle.json
bun src/cli.ts verify commitment.json bundle.json --range 100:200
```

`verify` 退出码：`0` 全部 ok；`1` 承诺无效或存在坏块；`3` 有缺块或证据不足。

## 布局

- `src/hash.ts` — SHA-256、域分隔、定长编码
- `src/tree.ts` — 树构建、证明生成/校验、承诺计算
- `src/verifier.ts` — 范围验证与四态报告
- `src/source.ts` — 块来源抽象；`FileChunkSource` 只按偏移定位读取请求块
- `src/prover.ts` / `src/bundle.ts` — 流式承诺、证明包序列化
- `src/cli.ts` — 命令行入口
- `test/` — 含攻击场景：等长块交换、总长度篡改、重复路径节点、跨版本拼证，
  以及“验证器不读取完整文件”的字节级计量测试
