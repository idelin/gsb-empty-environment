# 离线授权被复制到另一台充电桩

充电桩在断网时使用本地授权列表，攻击者复制整份列表到另一台设备，以他人账户免费充电。请实现供Go充电桩服务调用的可导入离线授权组件。授权项绑定桩标识、连接器、账户、额度与有效期并由后端签名，本地只存摘要与计数器；消费原子扣减并回传。复制、重放与超额拒绝。用列表复制、额度复用、过期授权、合法离线消费和回传验证授权只对原桩有效。

## 组件结构（包 `netsec`，模块 `example.org/netsec/q0384`）

- `types.go`：`Authorization`（桩标识/连接器/账户/额度/有效期）、`SignedToken`、`Receipt`、`SignedReceipt`、`StationConfig`。
- `encoding.go`：TLV 规范化编码、域分离前缀、Ed25519 签名载荷、SHA-256 授权摘要与收据哈希链，以及令牌/收据的线格式编解码。
- `backend.go`：后端签发授权、注册桩公钥、回传收据验证（签名、序列号、哈希链、绑定、有效期、累计额度）。
- `station.go`：充电桩侧可导入组件，安装授权列表、原子消费、回传队列与确认。
- `storage.go`：仅含摘要与计数器的本地状态、收据外发队列，0600 权限、临时文件 + rename 原子落盘。
- `errors.go`：全部安全拒绝的哨兵错误。

## 安全模型

- 后端用 Ed25519 对规范化授权签名；桩只信任预置的后端公钥，篡改令牌返回 `ErrBadSignature`。
- 授权签名载荷绑定 `StationID + ConnectorID + AccountID + Quota + 有效期`，异桩安装/消费返回 `ErrForeignStation`，连接器不匹配同样拒绝。
- 本地状态只保存授权的 SHA-256 摘要和剩余计数器（测试 `TestLocalStoreHoldsOnlyDigestsAndCounters` 断言不落明文），状态文件头部记录所属 `StationID`；整目录复制到另一台桩上 `OpenStation` 直接返回 `ErrCopiedStore`。
- 消费在互斥锁内完成“校验 → 扣减 → 生成签名收据 → 原子落盘”，超额返回 `ErrQuotaExceeded`，重复同步不会重置计数器。
- 每张收据带桩级单调序列号和 `PrevHash` 哈希链并由桩私钥签名；后端按序接收，重放返回 `ErrReplay`、乱序 `ErrSequenceGap`、断链 `ErrChainBroken`、超额 `ErrQuotaExceeded`。
- `InstallList` 是全量同步语义：新列表中缺失的授权被撤销；有效期外消费返回 `ErrExpired` / `ErrNotYetValid`。

## 调用方式

```go
backend, _ := netsec.NewBackend(backendPriv)
backend.RegisterStation("station-A", stationPub)
wire, _ := backend.Issue(netsec.Authorization{
    AuthID: "auth-1", StationID: "station-A", ConnectorID: "connector-2",
    AccountID: "account-42", Quota: 100,
    ValidFrom: from, ValidUntil: until,
})

station, _ := netsec.OpenStation(netsec.StationConfig{
    StationID: "station-A", PrivateKey: stationPriv,
    BackendKey: backend.PublicKey(), DataDir: "/var/lib/offline-auth",
})
_ = station.InstallList([][]byte{wire})

receipt, err := station.Consume(wire, "connector-2", 10) // 断网本地原子扣减
// 恢复联网后：
for _, r := range station.Flush() {
    encoded, _ := netsec.EncodeReceipt(r)
    if err := backend.ReceiveReceipt(encoded); err == nil {
        // 全部成功后确认
    }
}
_ = station.Ack(station.PendingReceiptCount())
```

## 测试

`go test -race ./...` 覆盖列表复制、额度复用与超额、过期/未生效授权、合法离线消费与回传、篡改令牌、并发原子扣减、重启持久化与哈希链延续、全量同步撤销、连接器绑定共 11 个场景。
