# netsec

供 Go 沙箱宿主使用的系统调用过滤器组合包。调用方只提供纯判定表，包负责组合、共享与全线程事务安装。

## 语义

- 动作优先级固定为 `ActionTerminate > ActionDeny > ActionNotify > ActionAllow`。
- 每次判定遍历整条过滤器链，取最高优先级动作；优先级相同时取最近安装的过滤器的 `Data`。
- 一个过滤器内的规则按顺序匹配，第一条命中的规则生效；无规则命中时使用 `Default`。
- 后安装的 `allow` 不能绕过早期 `deny`、`notify` 或 `terminate`。追加过滤器只会保持或收窄最终行为。
- `Chain` 不可变，可被多个线程安全共享；`Append` 返回新链，不修改旧链。
- 参数判定支持等值、比较、位掩码等纯谓词，不执行回调。

## 示例

```go
older := netsec.Filter{
    Rules: []netsec.Rule{
        netsec.DenyRule(1, 13), // syscall 1 -> EACCES
    },
    Default: netsec.ActionAllow,
}

newer := netsec.Filter{
    Rules: []netsec.Rule{
        netsec.AllowRule(1), // cannot override the older deny
        netsec.DenyRule(2, 1, netsec.ArgEquals(0, 7)),
    },
    Default: netsec.ActionAllow,
}

chain, err := netsec.NewChain(older, newer)
if err != nil {
    // handle invalid table
}
decision := chain.Evaluate(1, [6]uint64{})
// decision == netsec.Decision{Action: netsec.ActionDeny, Data: 13}
```

## 全线程安装

```go
host := netsec.NewHost(101, 102, 103)
host.SetInstallFunc(applyToRealThread) // 可选：宿主真正写入线程的钩子
host.SetRollbackFunc(restoreRealThread)

if err := host.Install(older, newer); err != nil {
    // 所有线程仍使用安装前的完整链
}
```

`Host.Install` 先组合并校验候选链，再逐线程应用。任一线程失败时，已成功线程按逆序回滚，`Host` 内部链指针与所有 `ThreadChain` 都保持旧值；失败后新建线程也只继承旧链。宿主钩子不得回调 `Host`，否则会与安装锁冲突。

## 有限域验证

`Enumerate` 枚举有限调用号与六个参数域的笛卡尔积，`VerifyNarrowing` 在枚举域上证明后一条链不会比前一条链更宽松。包内测试还会对每个可能的失败线程位置枚举调用与参数，确认失败安装不留下部分链：

```sh
go test ./...
go test -race ./...
go vet ./...
```
