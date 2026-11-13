# WPS PIN 分半验证的侧信道防护与失败锁定库

面向路由器固件 WPS 状态机的 C++17 静态库，解决外部注册协议（External Registrar）把 8 位 PIN
拆成前 4 位与后 3 位分别验证时的位置泄漏问题，并提供跨重启持久化的失败锁定。

## 威胁模型

接入点若在第一半错误时立即返回，攻击者可分别穷举两半，把 `10^7` 次尝试降到
`10^4 + 10^3 ≈ 11000` 次。本库保证：无论哪一半出错，接入点都执行完全相同的运算序列、
返回统一失败码，使响应内容与耗时均不泄漏出错位置。

## 公共 API（`netsec.hpp`）

- `Pin`：8 个数字，`parsePin` / `pinWithCheckDigit` / `computeCheckDigit` /
  `hasValidCheckDigit`，以及 `firstHalf()`、`secondHalf()`。
- `CommitmentScheme`：承诺派生抽象；内置实现 `HmacSha256CommitmentScheme`
  （自包含 SHA-256 + HMAC-SHA-256，无外部依赖），固件也可注入自有密码学后端。
- `RegistrarRequest`：注册方 MAC、随机数 nonce、两半承诺材料。
- `PinAuthenticator`：核心验证器，构造时传入设备 PIN、状态文件路径、`LockoutPolicy`、
  可选承诺方案与可注入时钟；入口为 `verify(request, submittedPin)`。
- `VerifyResult`：`Success` / `Failed` / `InvalidCheckDigit` / `InvalidInput` / `Locked`。

## 安全属性实现

- **校验位前置拒绝**：`computeCheckDigit` 按 `(奇位和*3 + 偶位和) % 10` 取 10 的补；
  校验位错误在进入承诺协议前直接返回 `InvalidCheckDigit`，不计入失败、不触发任何密码学运算。
- **两半不可区分**：`verify` 无条件先派生两半期望承诺，再用 `constantTimeEqual`
  （逐字节异或累积，无提前分支）比较，最后才合并为单个布尔结果。第一半错误时第二半运算
  照常完成，失败统一返回 `Failed`，不存在区分响应或提前返回。
- **失败锁定**：连续失败累计到阈值（默认 3）触发锁定；时长从 60 秒起，每个锁定事件
  翻倍，封顶 24 小时。锁定期对任何 PIN/任何 MAC 的请求在计算前直接返回 `Locked`，
  不调用承诺方案（测试用计数方案断言零调用）。
- **持久化与抗 MAC 重置**：状态写入二进制文件（小端、CRC32 完整性校验、临时文件
  `fsync` + 原子 `rename`）。重启后重新加载，失败计数与锁定窗口延续；状态不按 MAC 分键，
  更换注册方 MAC 不重置计数。文件缺失或损坏时按干净初始状态处理。
- **成功清零**：一次成功验证清零失败计数并解除当前锁定窗口；锁定层级保留，使后续锁定
  时长继续按既定序列翻倍。

## 状态文件布局（32 字节）

| 偏移 | 长度 | 字段 |
| --- | --- | --- |
| 0 | 4 | magic `NPIN` |
| 4 | 4 | version |
| 8 | 4 | failureCount |
| 12 | 4 | lockTier |
| 16 | 4 | lockStart（秒级时间戳） |
| 20 | 4 | lockDuration（秒） |
| 24 | 4 | 保留 |
| 28 | 4 | 前 28 字节的 CRC32-IEEE |

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

测试状态文件写入 `$TMPDIR`，不污染仓库。`test_netsec.cpp` 覆盖：

- 校验位公式与 PIN/MAC 解析、SHA-256/HMAC-SHA-256/CRC32 已知向量；
- 第一半错误与第二半错误返回同一 `Failed`，且两半承诺均被计算（计数断言）；
- 校验位错误在协议前拒绝且零密码学调用；
- 3 次失败锁定、锁定期零计算、60 秒起逐次翻倍至 24 小时封顶；
- 销毁并重建验证器模拟重启，锁定与计数延续；更换注册方 MAC 不重置；
- 损坏/截断状态文件回退到干净状态；
- 枚举全部 10000 个前半值，分别测量“第一半错”与“第二半错”各 10000 次的耗时，
  比较中位数与均值比例，并断言每次调用都完成了两半运算。
