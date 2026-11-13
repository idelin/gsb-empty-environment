# 键表达式包含与相交授权

Zenoh 路由节点的可导入 Go 授权包。规则分为 allow 与 deny，默认拒绝：

- `*` 匹配一个非空 chunk；
- `**` 匹配零个或多个 chunk；
- `$*` 匹配 chunk 内任意数量、不跨 `/` 的字符。

订阅表达式必须完整包含于**某一条** allow 规则，并且与**每一条** deny 规则都不相交。发布键必须是具体键，按同一组规则判定；包含通配符的发布键直接拒绝。

## 使用

```go
package main

import (
	"fmt"

	netsec "example.org/netsec/q0844"
)

func main() {
	auth, err := netsec.NewAuthorizer(
		[]string{"a/**"},
		[]string{"a/secret/*"},
	)
	if err != nil {
		panic(err)
	}

	fmt.Println(auth.AuthorizeSubscription("a/public/**")) // true
	fmt.Println(auth.AuthorizeSubscription("a/$*/*"))      // false: can cover a/secret/x
	fmt.Println(auth.AuthorizeSubscription("a/**"))        // false: intersects a/secret/*
	fmt.Println(auth.AuthorizePublish("a/secret/x"))       // false
}
```

`CheckSubscription` 和 `CheckPublish` 在拒绝时返回可用 `errors.Is` 判断的错误；非法表达式或非法规则不会被静默忽略。
