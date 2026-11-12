# Git replace引用阻断

仓库里的 refs/replace/<原对象> 能让 Git 在读取时把一个对象换成另一个，攻击者推送替换引用，让验证流水线看到的历史与已签名的历史不同。为Go仓库准入服务写供签名验证与构建前检查调用的 replace 阻断包：输入引用列表（名称与目标对象 ID）、调用方给出的提交图（每个提交的真实父提交）和已验签提交集合；refs/replace/ 下的任何引用一经出现就使其所列原对象视为不可信，其后代提交不得凭已验签集合通过，除非调用方显式给出 allowReplace 且该替换目标本身已验签；info/grafts 的内容同样视为替换；验证只读取原对象，不跟随替换；输出受替换影响的提交集合（原对象及其全部后代）。用替换已签名祖先、替换引用指向未验签提交、grafts 与 allowReplace 放行验证。

## 用法

```go
report := netsec.Analyze(refs, trueParents, signed, allowReplace)
if !report.OK() {
    // report.Affected 是原对象及全部后代。
    // report.Blocked 是应用 allowReplace 后仍不可信的提交。
    // report.Passed 是未被阻断的已验签提交。
}
```

`refs` 是 `[]netsec.Ref`。图中的父关系必须是调用方读取到的真实对象关系；检查器不会使用替换目标来遍历历史。`info/grafts` 可通过 `netsec.Input{GraftContents: ...}`、`Input.Grafts`，或名为 `info/grafts` 的伪引用传入。

普通替换只有在 `allowReplace` 为真且 `refs/replace/<原对象>` 的目标对象本身位于已验签集合中时才解除阻断。graft 没有可验签的替换对象，因此始终阻断。`report.Affected` 无论是否放行都会保留完整污染闭包，供构建前检查审计。
