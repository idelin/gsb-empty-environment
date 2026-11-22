# 密钥用途约束组件（keyusage）

为密钥服务实现的用途约束组件，位于 `keyusage` 包。每次密钥调用都必须先经过
`Enforcer.Check`，所有拒绝都会同步写入审计 `Sink`（默认 `MemorySink`）。

## 约束规则

1. **声明用途检查**：密钥只能执行其 `Usages` 中声明的用途，未声明则拒绝
   （`usage_not_declared`）。
2. **算法白名单**：仅白名单内算法可用（`IsWhitelisted`）；弱算法（如
   `3DES-CBC`、`RSA1_5`、`MD5`、`SHA1`、`RC4`、`DES-CBC`、`ECDSA-P192`）
   一律拒绝（`weak_algorithm`），未知算法拒绝（`algorithm_not_whitelisted`），
   算法不支持请求用途拒绝（`algorithm_usage_mismatch`）。
3. **用途族隔离**：签名族（`sign`/`verify`）与保密族（`encrypt`/`decrypt`/
   `wrap_key`/`unwrap_key`）互斥，签名密钥用于加解密或反之拒绝
   （`cross_family_usage`），登记时混用两族用途也会被拒。
4. **派生密钥**：父密钥必须声明 `derive`；派生密钥的用途只能是父用途的非空
   子集，越权继承拒绝（`derived_usage_exceeds_parent`），约束沿派生链逐级生效。
5. **包装密钥**：仅声明 `wrap_key`/`unwrap_key` 的密钥只能包装/解包其他密钥，
   加解密业务数据拒绝（`wrap_key_for_business_data`）。

## 主要类型

- `Key`：密钥声明（ID、算法、用途集合、可选父密钥 ID）。
- `Enforcer`：`Register` 登记密钥、`Derive` 派生子密钥、`Check` 强制关卡。
- `DenialError`：携带机器可读 `DenialReason` 的拒绝错误。
- `Event` / `Sink` / `MemorySink`：拒绝审计记录与可替换的接收器。

## 验证

测试覆盖错用（跨族/未声明）、弱算法与非白名单算法、算法能力不匹配、包装密钥
触碰业务数据、派生子集滥用与多级派生链：

```sh
go test ./... -race -count=1 -cover
```
