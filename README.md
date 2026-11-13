# 一次性授权码

一个可导入、并发安全的 Go 授权码状态机：

- 授权码只能成功兑换一次；单次兑换返回有效令牌。
- 同一授权码的第二次及之后兑换都失败，不会签发第二份令牌。
- 检测到重复兑换时，先成功兑换签发的令牌也会被撤销；之后该授权码保持撤销状态。
- 兑换、令牌签发和重放撤销在同一临界区内完成，避免并发竞态。

## 使用

```go
package main

import (
	"fmt"

	netsec "example.org/netsec/q0140"
)

func main() {
	m := netsec.NewMachine()
	if err := m.Issue("authorization-code"); err != nil {
		panic(err)
	}

	token, err := m.Redeem("authorization-code")
	if err != nil {
		panic(err)
	}
	fmt.Println(m.Validate(token))
}
```

状态迁移为 `issued -> redeemed -> revoked`。重复兑换会撤销该授权码下已经签发的全部令牌。授权服务也可以通过 `Apply(Event)` 送入 `issue`、`redeem`、`revoke-token` 或 `revoke-code` 事件。

行为对应 RFC 6749 §4.1.2 的要求：授权码不得使用多次；检测到重复使用时拒绝请求，并撤销此前基于该授权码签发的令牌。
