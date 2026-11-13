# 多设备安全号码

Go 客户端可导入的多设备安全号码包。安全号码不再只绑定账号主键，而是绑定每个账号完整、已校验的设备列表快照，因此服务端插入、隐藏、删除或重新加回设备都会改变会话号码。

## 安全绑定

- 设备列表按 `DeviceID` 字节序排序，每项编码 `DeviceID`、身份公钥和 `AddedVersion`；`Online` 不进入摘要，也不影响确认状态。
- 列表摘要使用固定域标签 `example.org/netsec/q0155/device-list/v1`，绑定 `accountID`、列表版本和全部设备项。
- 账号身份摘要使用固定域标签 `example.org/netsec/q0155/account-identity/v1`，绑定 `accountID`、列表版本、列表摘要和全部设备项。
- 两方会话对两个账号身份摘要按字节序排序、拼接后做 SHA256；原始 32 字节摘要就是二维码负载中的校验值。
- 原始校验值按大端整数模 `10^60` 并左补零，显示为 60 位十进制、12 组每组 5 位。

## 基本用法

```go
aliceList := netsec.MustNewDeviceListSnapshot("alice", 7, aliceDevices)
bobList := netsec.MustNewDeviceListSnapshot("bob", 3, bobDevices)

alice := netsec.NewClient("alice")
bob := netsec.NewClient("bob")
if err := alice.ImportSnapshots(aliceList, bobList); err != nil {
    log.Fatal(err)
}
if err := bob.ImportSnapshots(aliceList, bobList); err != nil {
    log.Fatal(err)
}

number, err := alice.SafetyNumber(bob)
fmt.Println(number.Display)

code, err := alice.GenerateQRCode("bob")
result, err := bob.VerifyQR("alice", code)
if err == nil && result.Status == netsec.StatusVerified {
    // 完整设备集合、列表版本/摘要和校验值全部一致。
}
```

`ImportSnapshot` 只接受通过结构一致性校验的完整快照：设备 ID 唯一，公钥非空，摘要字段必须与规范化内容一致。若调用方已有上一题的一致性校验，应先完成该检查再导入。

## 二维码负载

负载为紧凑 JSON，包含：

- `protocol_version`
- 两个 `account_id`
- 每方的 `list_version`、`list_digest` 和可选的 `account_digest`
- 原始 32 字节 `checksum`（十六进制）

验证要求账号集合与预期双方完全相同，每方当前列表版本、列表摘要和账号身份摘要与负载一致，并从当前本地快照重新计算校验值。参数顺序或二维码方向交换不影响结果。

## 状态与确认

- `Unverified`：尚未完成二维码验证或用户确认。
- `Verified`：两个客户端的完整设备集合、列表版本/摘要和校验值全部一致。
- `Stale`：已验证/已确认的绑定被更新的列表或设备身份变化取代，必须重新验证。
- `Mismatch`：同一列表版本下完整设备集合不一致。
- `Invalid`：二维码格式、账号集合或校验值无效。

确认记录 `ConfirmationRecord` 绑定双方列表摘要、列表版本、账号身份摘要、校验值、确认方式和 UTC 时间。设备新增、移除、公钥变化或 `AddedVersion` 变化都会撤销确认并显示 `Stale`；仅设备在线状态变化不会撤销。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖两个客户端持不同快照、服务端插入设备、隐藏设备、仅调整顺序、删除后再加同 ID、二维码方向交换、旧验证变 `Stale` 以及在线状态变化不撤销确认。
