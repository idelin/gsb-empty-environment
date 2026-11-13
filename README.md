# 反向引用放大

组内反向引用自己，匹配器会指数重试。组内引用自身或更外层尚未结束的组则拒绝。引用已经结束的组可以保留。策略宿主调用可导入的Go库做这次裁决。构造一个自引用组和一个合法回引。

## Go API

```go
import "example.org/netsec/q0365"

if err := netsec.Validate(pattern); err != nil {
	// Reject the pattern before passing it to the regex matcher.
}
```

`Validate` rejects a backreference whose target group is still open at the
reference site. This includes a group that refers to itself and a group that
refers to an unfinished outer group. A reference to a group that already
closed is accepted.

```go
netsec.Validate(`(a\1)`)          // rejected: group 1 is still open
netsec.Validate(`(?<x>a\k<x>)`)   // rejected: group x is still open
netsec.Validate(`(a)\1`)          // accepted: group 1 already closed
netsec.Validate(`(a(b)\2)`)       // accepted: group 2 already closed
```

The validator recognizes numeric (`\1`, `\g<1>`), named (`\k<name>`,
`\k'name'`, `\k{name}`, `\g<name>`), and Python-style (`(?P=name)`)
backreferences. `Analyze` additionally returns the group and reference
locations, while `IsSafe` provides a boolean result for policy hosts.
