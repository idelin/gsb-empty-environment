# PKCS#11会话对象导出禁止

PKCS#11代理要保证不可导出的密钥，包括会话对象，不能经包装、复制或属性读取泄漏。为Go HSM网关实现调用拦截包，供代理转发前调用：CKA_SENSITIVE为真时GetAttributeValue不返回CKA_VALUE；CKA_EXTRACTABLE为假的对象不得被C_WrapKey导出；CopyObject与SetAttributeValue不能把它由假改真。用会话对象、复制、包装验证。

## 实现

`netsec.Guard` 实现 `Backend` 转发前的调用拦截，四个入口对应 PKCS#11 函数：

- `GetAttributeValue`：目标对象 `CKA_SENSITIVE=true` 时，请求 `CKA_VALUE`
  （以及私钥分量等秘密属性）直接返回 `ErrAttributeSensitive`
  （`CKR_ATTRIBUTE_SENSITIVE`），不转发；非秘密属性正常透传。
- `WrapKey`：目标对象 `CKA_EXTRACTABLE=false` 时返回
  `ErrKeyUnextractable`（`CKR_KEY_UNEXTRACTABLE`），后端不会收到调用。
- `CopyObject` / `SetAttributeValue`：模板试图把 `CKA_EXTRACTABLE`
  由假改真时返回 `ErrTemplateInconsistent`（`CKR_TEMPLATE_INCONSISTENT`）。

拦截器自身不保存对象状态，每次决策向后端查询当前属性，因此会话对象
（session object）与令牌对象适用同一策略；属性读取或 `CK_BBOOL` 解码
失败一律 fail-closed，拒绝转发。

## 用法

```go
guard := netsec.NewGuard(hsmBackend) // hsmBackend 实现 netsec.Backend

attrs, err := guard.GetAttributeValue(session, key, []netsec.AttributeType{netsec.CKA_VALUE})
wrapped, err := guard.WrapKey(session, wrappingKey, key, netsec.Mechanism{Mechanism: mech})
_, err = guard.CopyObject(session, key, template)
err = guard.SetAttributeValue(session, key, template)
```

## 验证

```sh
go test ./... -race -cover
```

测试用内存 HSM 桩分别建立会话对象与令牌对象，覆盖敏感属性读取阻断、
混合模板阻断、不可导出对象包装阻断、复制/改属性的 `CKA_EXTRACTABLE`
翻转阻断、合法路径透传、会话关闭后句柄失效，以及后端不可用时的
fail-closed 行为。
