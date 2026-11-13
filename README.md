# 混合密钥封装的单分量替换

`netsec` 是一个可导入的 Go 包（module `example.org/netsec/q0062`，包名
`netsec`），为握手宿主提供绑定两份密文的混合 KEM 解封装。

## 注册与输入

- 宿主通过 `Registry.RegisterSuite` 按固定 8 字节 `suiteID` 注册两个
  `Adapter`，同时在 `SuiteConfig` 中固定两个分量的规范密文长度。
- `Adapter` 对任何规范长度密文都必须返回 32 字节共享秘密和 `valid` 布尔值；
  失败时返回隐式拒绝（implicit rejection）秘密而不是错误或空串。
- 线格式严格为：`suiteID(8) || len(ct1, uint16 BE) || ct1 ||
  len(ct2, uint16 BE) || ct2 || 客户端公钥摘要(32) || 服务端公钥摘要(32) ||
  握手上下文摘要(32)`，不允许尾随字节，由 `ParseRequest`/`Request.Encode`
  处理。
- 长度不匹配、未知 `suiteID`、截断或尾随字节都在调用任何适配器之前拒绝，
  因此适配器只会看到注册时声明的规范长度。

## 派生规则

无论 `valid` 真假，两个适配器都会被调用，并始终使用两份秘密：

```
PRK = HMAC-SHA256(0x00*32, ss1 || ss2)
key = HMAC-SHA256(PRK, label || suiteID ||
                   uint16(len(ct1)) || ct1 ||
                   uint16(len(ct2)) || ct2 ||
                   clientPubHash || serverPubHash || contextHash)
```

密文长度以 uint16 大端前缀参与派生，因此“失败分量当空串”或替换任一分量
都会改变内部派生值。只有 `valid1 AND valid2` 为真时才把派生密钥交给上层；
否则返回与成功密钥等长的随机通用失败密钥以及统一错误
`ErrDecapsulationFailed`，不区分哪个分量失败。内部诊断仅以
`FailureMask`（`MaskComponent1`/`MaskComponent2`）记录失败位掩码。

## 安全边界

- 禁止降级：任何情况下都不会只用单个有效分量派生密钥，失败分量以其隐式
  拒绝秘密继续参与完整派生。
- 本包只保证组合规则、严格输入处理与转录绑定；**不宣称宿主注册的适配器
  自身的密码实现已经过验证**。

## 测试

`go test -race ./...` 覆盖：计数适配器（验证任何路径下两个适配器都被
调用）、交换密文顺序、替换任一分量、同秘密不同密文、摘要/上下文/suiteID
绑定、单侧与双侧失败的统一外部失败与随机失败密钥、适配器前拒绝、注册
校验、并发安全，以及固定 KAT 向量与内部位掩码诊断。
