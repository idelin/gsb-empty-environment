# 填充反馈差异的可重放验证

这是一个仅用于本地安全回归的 Go 实验包，不连接外部服务。测试宿主创建一个
AES-128-CBC 解密回调，攻击侧只持有该回调，不会接收或读取目标密钥。

## 回调契约

回调输入是 16 字节 IV 和至少一个 16 字节正文块，输出只能是：

- `PaddingValid`：末字节为 `k` 且末 `k` 字节均为 `k`，`1 <= k <= 16`。
- `PaddingInvalid`：PKCS#7 填充无效。
- `InfrastructureError`：无法给出可靠填充结果，例如结构错误。
- `Rejected`：认证修复回调对未经认证请求的统一拒绝。

旧式回调绝不返回明文。AES-128 密钥由测试通过
`NewAESCBCPaddingOracle(key)` 封存在闭包中；恢复函数 `RecoverLastBlock`
只接收回调、原始 IV、正文和查询预算，因此攻击路径没有读取密钥的入口。

## 末块恢复

```go
oracle, err := netsec.NewAESCBCPaddingOracle(randomAES128Key)
result, err := netsec.RecoverLastBlock(oracle, iv, ciphertextBody, budget)
```

恢复器从最后一块倒数第一个字节开始：

1. 对每个位置完整扫描全部 256 个猜测，不会在第一个 `PaddingValid` 处停止。
2. 最后字节的每个有效候选再用两个不同的前一字节探测值复核。真正的单字节
   填充在所有探测下都保持有效；由原始 `02 02`、完整 `0x10` 填充块或其他
   多字节合法填充产生的候选至少会被其中一个探测排除。
3. 已恢复的右侧字节固定为当前所需填充长度，因此后续每个位置理论上只有
   唯一有效猜测；出现零个或多个有效猜测时拒绝报告明文。
4. 最后用中间值与原前一密文块异或，得到最后一块明文。

每次逻辑查询默认在基础设施错误后最多重试一次；重试上限可由
`RecoverLastBlockWithConfig` 调整，但硬上限为 5 次。基础设施错误绝不会被
当成 `PaddingInvalid`。持续失败会立即中止，并保留部分中间值和未知字节掩码。

`Result` 包含：

- `Intermediate`：已恢复的最后一块中间值字节，未恢复位置为零。
- `PlaintextLastBlock`：仅在完整恢复时出现，否则为 `nil`。
- `UnknownMask`：`true` 表示该中间值字节仍未知。
- `QueryLog`：每次实际回调的 IV、正文、状态、重试序号和阶段。
- `QueriesUsed`：实际调用次数，严格不超过 `Budget`。
- `Complete`、`OracleEstablished`、`StopReason`。

查询日志可直接重放：

```go
if err := netsec.Replay(result.QueryLog, oracle); err != nil {
    // 记录与回调行为不一致
}
```

预算可以恰好等于一次成功运行的 `QueriesUsed`：最后一条查询完成即返回成功，
不会额外探测。少一条预算则以 `ErrBudgetExhausted` 中止，并返回截至预算边界的
日志与未知字节掩码。

## 认证修复回调

`NewAuthenticatedCBCPaddingOracle` 为宿主提供的认证目标对计算
HMAC-SHA256（覆盖 `IV || ciphertext`）。每次请求先验签，再检查长度并解密：

- 任何未经认证的改动，包括 IV、正文、截断、追加及畸形长度，都只得到
  `Rejected`。
- 只有验签通过的目标数据才可能进入 AES 解密和填充检查。

```go
fixed, err := netsec.NewAuthenticatedCBCPaddingOracle(encKey, macKey, iv, body)
result, err := netsec.RecoverLastBlock(fixed, iv, body, budget)
if errors.Is(err, netsec.ErrOracleNotEstablished) {
    // 无法建立填充预言；result.PlaintextLastBlock == nil
}
```

认证实验不会把统一拒绝解释为可用的填充区分，也不会恢复或猜测错误明文。
该回调是回归夹具，不是通用消息加密 API；生产场景应使用成熟 AEAD。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试使用随机 AES-128 密钥，覆盖原文末尾为 `01`、`0202`、完整 `0x10`
填充块、预算恰好耗尽与差一、完整及部分日志重放、瞬时与持续基础设施错误、
以及认证回调下的“无法建立预言”路径。外部测试包只向攻击函数传递回调，
不传目标密钥。
