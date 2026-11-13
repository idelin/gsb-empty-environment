# 签名否定区间缓存

`netsec` 是面向递归解析宿主的可导入 Go 包，只保存调用方已经验证的 NSEC
记录，并用它们安全回答同一固定 zone 内的否定查询。

## 使用

```go
cache, err := netsec.NewCache("example.", netsec.WithClock(clock))
if err != nil {
    // ...
}

record := netsec.NSECRecord{
    Owner:            "example.",
    Next:             "www.example.",
    Types:            netsec.TypeBitmap{netsec.TypeSOA, netsec.TypeNSEC},
    TTL:              300,
    SignatureExpires: rrsigExpiry,
    ReceiptID:        receipt.ID,
    ZoneSerial:       receipt.ZoneSerial,
}
if err := cache.Add(record, receipt); err != nil {
    // mismatch, expired, stale serial, or out-of-zone record
}

answer, err := cache.Query("missing.example.", netsec.TypeA)
switch answer.Status {
case netsec.NoData:
    // answer.Proof contains the exact-owner NSEC.
case netsec.NXDomain:
    // answer.Proof contains the minimum query-name/wildcard proof set.
case netsec.NeedUpstream:
    // the cache does not have enough current evidence.
}
```

`Receipt` 必须绑定验证时的 zone、owner、next、类型位图、RRSIG 过期时刻和
zone serial；任一字段不一致都会拒绝写入。有效期为“插入时刻 + 原始 TTL”
与 RRSIG 过期的较早者。

## 查询规则

- 名称统一转为小写、规范 presentation 格式；比较遵循 DNSSEC 规范，从根侧
  标签到叶侧标签逐字节进行。zone 外名称和 zone 的父名均被拒绝。
- NSEC 区间为严格区间 `(owner, next)`。`owner == next` 覆盖整个 zone 环，
  但 owner 自身除外；`owner > next` 时区间跨过规范顺序末尾。
- 查询名精确等于 owner 时，只有位图缺少请求类型（且没有 CNAME 阻断）才
  返回 `NoData`。若覆盖查询名的 NSEC 的 next 是查询名的严格子域，则查询名
  被证明为空非终结，也可直接返回 `NoData`。
- `NXDomain` 必须同时具备严格覆盖查询名的当前 NSEC，以及严格覆盖
  `*.<closest-encloser>` 的当前 NSEC。closest-encloser 只由有效 NSEC 的
  owner、next 及其祖先（含空非终结）逐层推导，不从区间内部猜测。
- 无法证明的空非终结、已存在的通配符、过期证据、缺失覆盖记录或跨验证
  代次的证据不足时返回 `NeedUpstream`。
- 证明结果 `Answer.Proof` 只含实际使用的记录，去重后按 owner 的 DNSSEC
  规范顺序输出。
- 同 serial 的多条 NSEC 可以组合。收到更高 serial 时旧记录立即整体清除；
  serial 按 RFC 1982 比较，较旧记录被拒绝。

测试包含完整三标签、每层二值名字全集（`z.`、`a/b.z.`、`a/b.a/b.z.`）
的参考模型核对，并覆盖环绕、边界、空非终结、通配符存在、NODATA/NXDOMAIN
区分、TTL/RRSIG 过期和 serial 换代。
