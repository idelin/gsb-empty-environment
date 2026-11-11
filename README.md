# pwupgrade

登录服务里的旧账户仍存着加盐SHA-1，迁移要在用户登录成功后透明升级。给Go登录处理器实现口令哈希包：解析旧SHA-1和带迭代次数的PBKDF2-SHA256格式并常数时间比较；验证成功后按当前参数重新哈希，用比较交换写回，若期间用户已改密则放弃升级；未知格式和越界参数返回错误而不崩溃。用并发改密与登录的交错测试，确认新口令不会被覆盖。

## 存储格式

- 旧格式（加盐 SHA-1）：`sha1$<salt-base64>$<hash-base64>`
- 当前格式：`pbkdf2_sha256$<iterations>$<salt-base64>$<key-base64>`
- Base64 使用无填充标准字母表（`RawStdEncoding`）。

当前参数：PBKDF2-HMAC-SHA256，600000 次迭代，16 字节随机盐，32 字节派生密钥。

## 处理器接入

```go
// store 实现 pwupgrade.UserStore（GetHash / CompareAndSwapHash / SetPassword）
ok, err := pwupgrade.VerifyAndUpgrade(store, userID, password)
```

- 口令比较走 `crypto/subtle.ConstantTimeCompare`，错误口令返回 `(false, nil)`。
- 验证成功但哈希是 SHA-1 或迭代次数过低时，按当前参数重新哈希。
- 写回采用比较交换（CAS）：仅当存储值仍等于登录时读到的旧哈希才写入；
  期间用户已改密则 CAS 失败并静默放弃升级，绝不覆盖新口令。
- 重新哈希失败不影响本次登录结果，下次登录仍会再次尝试升级。

## 非法输入

未知算法返回 `ErrUnknownFormat`；结构损坏返回 `ErrMalformedHash`；
迭代次数非正数或超过 `10^7`、盐/密钥长度越界返回 `ErrInvalidParameters`；
用户不存在返回 `ErrNotFound`。解析路径不分配超大缓冲区，不会 panic。

## 测试

```sh
go test -race -count=1 ./...
```

`TestUpgradeAbortsWhenPasswordChanged` 通过 CAS 前钩子确定性地交错改密；
`TestConcurrentLoginAndChange` 以 4 个登录 goroutine 与 25 次轮换改密并发压测，
断言最终哈希恰好等于最后一次改密写入，旧登录升级不会覆盖新口令。
