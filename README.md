# PAM认证栈旁路推导

PAM 配置里一行 sufficient 放错位置，就可能让任何口令都通过登录。为Go配置分析包写可导入的 auth 栈判定：control 支持 required、requisite、sufficient、optional 与 [success=N default=bad]；required 失败后继续执行但终局失败，sufficient 成功且此前无 required 失败即通过；枚举模块成败组合，找出口令模块失败仍通过的路径。用 pam_permit 排在 pam_unix 前、requisite 和 success=1 验证。

## 用法

```go
package main

import (
    "fmt"
    "strings"

    netsec "example.org/netsec/q0836"
)

func main() {
    config := strings.NewReader(`
auth sufficient pam_permit.so
auth required   pam_unix.so
`)

    rules, err := netsec.ParseAuthStack(config)
    if err != nil {
        panic(err)
    }

    for _, path := range netsec.PasswordBypassPaths(rules) {
        fmt.Printf("bypass: outcomes=%v actual=%v decision=%s\n",
            path.Outcomes, path.ActualOutcomes(), path.Decision)
    }
}
```

## 判定语义

- `required`：失败后继续执行后续模块，但栈终局为拒绝。
- `requisite`：失败后立即终止并拒绝。
- `sufficient`：成功且此前没有 required 失败时立即通过；失败则忽略并继续。
- `optional`：忽略成败；仅当它是栈中唯一模块时，成功通过、失败拒绝。
- 显式控制：支持 `ignore`、`ok`、`bad`、`die`、`done`、`reset` 和正整数 `N` 跳转；未配置的返回码默认按 `bad` 处理。
- `success=N` 只跳过后续 `N` 个模块，本身不把栈结果记为成功。
- `EnumerateAuthPaths` 枚举每个模块成功或失败的全部组合；`PasswordBypassPaths` 筛选最终通过且 `pam_unix.so` 被赋为失败的组合。

## 验证

```sh
go test ./...
```
