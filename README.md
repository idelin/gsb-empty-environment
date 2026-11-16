# mdoc设备认证会话转录与摘要核对

移动驾照的验证端收到的响应里，攻击者可以转发另一个验证端会话中拿到的响应（中继），或夹带发行方没签过的数据项。给Go验证端实现可导入的 mdoc 响应校验包，输入已解码的 IssuerSigned（MSO 的 valueDigests 与摘要算法、validityInfo、DocType，以及各命名空间下每个数据项的 digestID、random、elementIdentifier、elementValue）、DeviceSigned（deviceNameSpaces 与设备签名）、验证端自己的 SessionTranscript 字节和请求的数据项集合，签名与哈希由回调提供：每个返回的数据项都要在 MSO 里按 digestID 找到摘要，且与重算结果相等；返回项必须是请求项的子集；validityInfo 的 validFrom 与 validUntil 须包含当前时刻；设备签名的待签内容是数组 ["DeviceAuthentication", SessionTranscript, DocType, DeviceNameSpacesBytes] 的规范编码，其中的 SessionTranscript 必须是本端自己的；MSO 里的 DocType 须与响应中的一致；同一 digestID 重复出现、random 短于 16 字节时拒绝。用中继他人转录、夹带未签名数据项、超出请求集合、过期 MSO 和重复摘要标识验证。

## 包结构

可导入包位于 `mdoc/`，模块路径为 `example.org/netsec/q0720/mdoc`，仅依赖 Go 标准库：

- `mdoc/types.go`：已解码的 `Response`、`IssuerSigned`、`MSO`、`DeviceSigned`、
  `RequestedItems` 等输入类型。
- `mdoc/cbor.go`：确定性 CBOR 编码器（RFC 8949 核心确定性编码规则，map 键按
  编码字节序排序）与 CBOR 合法性校验器，用于重算摘要和重建待签内容。
- `mdoc/verify.go`：`Verifier.Verify` 入口、回调类型和分类哨兵错误。
- `mdoc/*_test.go`：中继、夹带、越权请求、过期、重复 digestID 等场景测试。

## 校验语义

`Verifier` 通过两个回调与本地密码学栈解耦：

- `Hash(algorithm, data)`：按 MSO 的 digestAlgorithm（如 `SHA-256`）计算摘要。
- `VerifyDeviceSignature(authBytes, sig)`：对重建出的 DeviceAuthentication 字节
  验证 COSE_Sign1（由调用方绑定 MSO 中的 deviceKey 并校验 COSE 头）。

`Verify` 依次保证：

1. 响应 DocType 与 MSO DocType 完全一致。
2. 当前时刻落在 `[validFrom, validUntil]` 闭区间内（`Now` 可注入）。
3. 本端 SessionTranscript 非空且为合法 CBOR，并逐字节嵌入待签数组；来自其他
   会话的中继响应无法通过设备签名验证。
4. 每个 IssuerSignedItem 的 random 不少于 16 字节，且同一命名空间内 digestID
   不重复（不同命名空间允许复用相同编号）。
5. 每个返回的 elementIdentifier 都在 `RequestedItems` 内，即返回项是请求项的
   子集（选择性披露允许返回更少）。
6. 每个数据项按 digestID 在 MSO `valueDigests` 同命名空间下命中摘要；重算
   `#6.24(bstr .cbor IssuerSignedItem)` 的摘要并以恒定时间比较，夹带未签名或
   被篡改的数据项会得到 `ErrDigestNotFound` / `ErrDigestMismatch`。
7. 设备签名覆盖规范编码的
   `["DeviceAuthentication", SessionTranscript, DocType, #6.24(bstr .cbor DeviceNameSpaces)]`。

错误以哨兵值导出（`ErrExpired`、`ErrDigestMismatch`、`ErrDeviceAuthentication`
等），均可用 `errors.Is` 匹配，外层附带命名空间、digestID 等定位上下文。

## 运行测试

```sh
go test ./... -race -cover
```
