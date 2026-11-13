# 预授权交易码

可由发行服务导入的 Go 库，实现 OpenID4VCI 预授权码（pre-authorized code）流程中的
`tx_code`（transaction code）校验。

## 行为约定

- 每个预授权码独立绑定自己的 `tx_code`，不存在全局或可跨码复用的 `tx_code`。
- 发行时要求 `tx_code` 的预授权码，兑换时必须提供与该码绑定的值。
- 缺失 `tx_code`、值错误、或使用另一个预授权码的 `tx_code`，兑换都会失败。
- 发行时不要求 `tx_code` 的预授权码可以直接兑换。
- 兑换成功即原子消费该码，同一预授权码不能重复兑换。
- `tx_code` 使用 SHA-256 后做常量时间比较，避免按比较时长泄露信息。
- 交换两个预授权码的 `tx_code` 时，两码的绑定值原子交换；任一码不存在、已使用、
  已过期或未绑定 `tx_code` 时不做任何修改。
- 发行服务应在兑换入口按预授权码和客户端限流，防止对短 `tx_code` 的暴力尝试。

## 用法

```go
package main

import (
	"errors"
	"fmt"

	"example.org/netsec/q0119"
)

func main() {
	issuer := netsec.NewIssuer()

	// 需要 tx_code 的预授权码。tx_code 通过独立渠道交给最终用户。
	issued, err := issuer.Issue(netsec.IssueOptions{
		RequireTxCode: true,
		TxCode:        "493536",
	})
	if err != nil {
		panic(err)
	}

	// 缺少或错误 tx_code 都返回错误。
	if err := issuer.Redeem(issued.Code, ""); err != nil {
		fmt.Println("missing tx_code:", err)
	}
	if err := issuer.Redeem(issued.Code, "111111"); !errors.Is(err, netsec.ErrTxCodeMismatch) {
		panic(err)
	}

	// 正确绑定值兑换成功，并且只能成功一次。
	if err := issuer.Redeem(issued.Code, "493536"); err != nil {
		panic(err)
	}
}
```

不要求 `tx_code` 时直接兑换：

```go
code, err := issuer.IssueWithTxCode("")
if err != nil {
	panic(err)
}
err = issuer.Redeem(code, "")
```

交换两个预授权码的 `tx_code`：

```go
err := issuer.SwapTxCodes(codeA, codeB)
```

## API

- `NewService` / `NewIssuer` / `NewStore`：创建发行服务实例。
- `Issue(IssueOptions)`：发行预授权码；非空 `TxCode` 会自动视为要求校验，
  需要 `tx_code` 且未提供值时会生成并返回。
- `IssueWithTxCode(txCode)`：非空 `txCode` 表示必须校验，空值表示可直接兑换。
- `IssuePreAuthorizedCode(requireTxCode, txCode)`：显式指定是否需要 `tx_code`。
- `Redeem(code, txCode)` / `RedeemPreAuthorizedCode(...)`：校验并原子消费预授权码。
- `SwapTxCodes(codeA, codeB)`：原子交换两个码绑定的 `tx_code`。
- `VerifyTxCode(required, bound, presented)`：供自行持久化绑定的发行服务做纯校验。
- `Issue` / `Redeem` / `SwapTxCodes` 等包级函数：使用 `Default()` 进程级实例。

兑换失败均可用 `errors.Is(err, netsec.ErrInvalidGrant)` 判断；对钱包应统一返回
OAuth `invalid_grant`，不要区分码不存在、过期、已兑换和 `tx_code` 不匹配。

## 测试

```sh
go test ./...
go vet ./...
```
