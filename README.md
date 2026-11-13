# RSA 奇偶性泄露回归包

本包只攻击**本地测试回调**：宿主用合法的 RSA 密钥生成公开的奇数模数 `N`、指数 `e` 和目标密文 `c = m^e mod N`，并只在闭包里持有私钥。包本身只接收 `0 <= x < N` 上的奇偶性回调，不读取私钥。

## 攻击语义

- 第一次查询为 `c * 2^e mod N`，之后每次把密文乘以 `2^e mod N`，对应解密值 `2^i * m mod N`。
- 用整数端点 `[lo/2^step, hi/2^step]` 精确收缩，不使用任何浮点数。
- 偶数和奇数分别收缩到左、右半区间。
- 暂时不可用或认证拒绝会消耗一次查询预算，但保持当前查询和区间不变，下一次重试同一查询。
- 区间只剩一个整数候选时，必须验证 `m^e mod N = c`，验证通过才返回 `StatusSuccess`。
- 预算耗尽返回 JSON 可序列化检查点。恢复时校验 `N`、`e`、`c`、完整历史摘要，并重放每条查询及区间转移。
- `c = 0` 直接验证明文 `0`；同样走 `m^e mod N = c` 校验。

## 宿主调用

```go
key, _ := rsa.GenerateKey(rand.Reader, 2048)
e := big.NewInt(int64(key.E))
m, _ := rand.Int(rand.Reader, key.N)
c := new(big.Int).Exp(m, e, key.N)

// 私钥只存在于宿主回调闭包中。
oracle := netsec.ParityOracleFunc(func(x *big.Int) (netsec.OracleResult, error) {
    if err := netsec.CheckQuery(key.N, x); err != nil {
        return netsec.ResultRejected, err
    }
    y := new(big.Int).Exp(x, key.D, key.N)
    if y.Bit(0) == 1 {
        return netsec.ResultOdd, nil
    }
    return netsec.ResultEven, nil
})

out, err := netsec.Recover(netsec.Config{
    N: key.N, E: e, C: c, MaxQueries: key.N.BitLen() + 16,
}, oracle)
if err == nil && out.Succeeded() {
    // out.Recovered == m，且已重新加密核对
}
```

`MaxQueries` 统计所有回调调用，包括不可用和被拒绝的调用。预算耗尽时：

```go
raw, _ := netsec.EncodeCheckpoint(out.Checkpoint)
cp, _ := netsec.DecodeCheckpoint(raw)
next, err := netsec.RecoverFromCheckpoint(netsec.Config{
    N: key.N, E: e, C: c, MaxQueries: key.N.BitLen() + 64,
}, cp, oracle)
```

## 修复回调

`NewAuthenticatedOracle` 在调用解密/奇偶函数前先检查认证。认证失败统一返回 `ResultRejected`，该值在类型上不同于 `ResultEven`，攻击器不会把它当偶数。`Decryptions()` 可用于回归断言认证失败时解密函数从未被调用。

```go
fixed, _ := netsec.NewAuthenticatedOracle(key.N,
    func() bool { return authenticated },
    func(x *big.Int) (netsec.OracleResult, error) {
        // 只有认证通过后才会到达这里。
        ...
    })
```

对始终拒绝的修复回调运行恢复，会以 `StatusNoUsableOracle` 结束并返回检查点，而不会产生明文或成功状态。

## 测试覆盖

- 多个小合数模数上穷举全部明文；独立重算每次查询与精确有理端点。
- `c = 0`、`m = N-1`、随机 `m`。
- 宿主生成的 1024 位 RSA 密钥随机验证。
- 连续/间歇暂时不可用、预算中断与检查点恢复。
- 检查点参数、摘要、历史、区间和待查询密文的篡改检测。
- 非法配置、域外输入、非法回调结果、唯一候选验证失败。
- 认证前拒绝回调：统一拒绝不被当作偶数，解密次数保持为零。

日志 `LogEvent` 只包含可观察查询、结果和由结果推导的精确区间端点；不记录回调内部明文。
