# 通用链接components匹配

iOS 通用链接的 apple-app-site-association 里 components 的顺序和 exclude 决定哪些路径打开应用，攻击者用 /share/../account 或带查询参数的地址诱导打开受保护页面。为Go链接服务实现可导入的路径匹配包：调用方给出本应用的 appID，details 的 appIDs 为 TEAMID.bundle 形式，团队 ID 不符则忽略该项；components 逐条按顺序匹配，首条命中决定，exclude 为 true 则不打开应用；/ 字段用 * 匹配任意字符序列、? 匹配单个字符，? 字段匹配查询串，# 字段匹配片段；URL 路径先消去点段再匹配，%2F 与重复斜杠拒绝。用点段、exclude 顺序、通配与查询匹配验证。

## 包 `universallink`

导入路径：`example.org/netsec/q0183/universallink`

```go
package main

import (
    "encoding/json"
    "fmt"
    "log"

    "example.org/netsec/q0183/universallink"
)

func main() {
    aasaBytes := []byte(`{"applinks":{"details":[{"appIDs":["ABCD1234.com.example.Links"],"components":[{"/":"/account*","exclude":true},{"/":"*"}]}]}}`)
    var aasa universallink.Association
    if err := json.Unmarshal(aasaBytes, &aasa); err != nil {
        log.Fatal(err)
    }

    matcher, err := universallink.NewMatcher("ABCD1234.com.example.Links", &aasa)
    if err != nil {
        log.Fatal(err)
    }

    // /share/../account 先消去点段变为 /account，命中 exclude 规则。
    open, err := matcher.ShouldOpen("https://example.com/share/../account")
    fmt.Println(open, err) // false <nil>
}
```

### 语义要点

- `NewMatcher(ownAppID, aasa)`：只保留 appID 团队 ID（首个 `.` 之前）与本应用一致的 detail 项，其余忽略。
- `Decide` 返回 `DecisionOpen` / `DecisionExcluded` / `DecisionNoMatch`；`ShouldOpen` 是其布尔封装。
- components 按文档顺序逐条匹配，首条命中即决定结果；缺省的 `/`、`?`、`#` 字段表示该部分无约束。
- 通配：`*` 匹配任意长度字符序列（含空），`?` 匹配单个字符，其余字符按字面量精确匹配。
- 路径安全处理：先拒绝 `%2F`/`%2f` 与重复斜杠（返回带哨兵错误 `ErrEncodedSlash`、`ErrDuplicateSlash` 的 error），再按 RFC 3986 5.2.4 消去点段后匹配。
- `?` 查询匹配：键为字面量、值支持通配；要求的键必须存在且至少一个值匹配，额外查询键允许存在，重复键保留。
- `#` 片段对 URL 解码后的 fragment 做通配匹配。
