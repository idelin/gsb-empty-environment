# 前缀有效性预言的多区间收缩

`example.org/netsec/q0031` 是可导入的 Go 恢复包。它只调用本地夹具提供的谓词预言，不访问网络，也不会把尚未通过重加密校验的候选值当作明文输出。

## 模型

- 本地合法 RSA 公钥 `(N, e)`，`k = len(N.Bytes()) ≥ 3`。
- `B = 2^(8*(k-2))`，目标明文满足 `2B ≤ m < 3B`。
- 预言只对 `x` 返回以下三者之一：`Dec(x)` 落入 `[2B, 3B)`、不落入、暂时不可用。
- 查询形式为 `x = c*s^e mod N`。候选集是精确大整数闭区间；一次反馈可以产生多个区间，相邻区间会自动合并。

## 基本用法

```go
key, err := netsec.NewPublicKey(n, e)
if err != nil {
    return err
}

session, err := netsec.NewSession(key, c, netsec.OracleFunc(func(x *big.Int) (netsec.Feedback, error) {
    // 本地夹具：只返回 yes/no/unavailable，不返回解密值。
    return fixture.PrefixCheck(x)
}), budget)
if err != nil {
    return err
}

out, err := session.Recover()
if err != nil {
    return err
}
if out.Status == netsec.StatusRecovered {
    // out.Plaintext 只有在重加密得到 c 后才会非 nil。
}
```

`Step` 每次最多查询一次。暂时不可用仍消耗预算，但候选区间和下一搜索位置不变；预算耗尽时 `Outcome.Intervals` 是当前完整候选集，`Outcome.NextS` 给出下一搜索位置。

## 区间更新

`FilterIntervals(intervals, s, N, B, feedback)` 对每个当前区间计算可达整数商 `q` 的范围，并与

```text
[ceil((q*N+2B)/s), ceil((q*N+3B)/s)-1]
```

求交或做差，然后排序并合并相邻区间。它遍历商区间，不遍历候选明文。所有端点和加法、取模运算均使用 `math/big.Int`。

会话内部对三字节小模数（完整回归范围）会精确应用每一次 yes/no。对更大模数，为避免否定反馈把候选集打碎成过多片段，超过内部表示上限的否定反馈会保守地保留原候选集；该策略只会保留一个超集，不会引入错误候选，最终成功仍必须通过重加密校验。低层 `FilterIntervals` 本身始终精确执行否定反馈的区间求差。

## 检查点

`Session.Checkpoint()` 生成带 SHA-256 绑定的 `Checkpoint`，绑定 `N`、`e`、目标密文、`K/B`、完整候选区间、下一搜索位置和已记录查询。`RestoreSession` / `ResumeSession` 会校验绑定，并从查询日志重新推导区间；日志与存储区间不一致、查询密文不是 `c*s^e mod N`、或日志导致空候选集时返回 `ErrCheckpointMismatch` / `ErrInconsistent`。

`Checkpoint.WithBudget` 或 `RestoreSessionWithBudget` 可在恢复时延长预算。

## 一致性

- 空交集、同一乘数的矛盾 yes/no、非法反馈值都返回 `ErrInconsistent` 或包装它的错误。
- 最后只剩一个整数时仍会执行 `m^e mod N == c`。不匹配即返回不一致，不发布明文猜测。
- 测试使用小 RSA 模数逐个枚举 `[2B, 3B)` 内明文，覆盖多区间、`2B`/`3B-1` 边界、空交集、预算中断及检查点恢复。
