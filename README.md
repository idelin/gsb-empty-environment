# DDS 权限规则判定

供 DDS Security 访问控制插件调用的 Go 权限判定包。调用方先验证权限文档的
XML 签名；本包负责把已验签文档绑定到身份证书，并对发布/订阅请求做判定。

```go
doc, err := netsec.ParsePermissions(permissionsXML, certSubject, time.Now())
if err != nil {
    // 文档无效：主体不符、过期/尚未生效或格式错误。访问控制应直接拒绝。
}

decision := doc.Authorize(netsec.Request{
    DomainID:   0,
    Topic:      "rt/chatter",
    Partition:  "Prod",
    Operation:  netsec.Publish,
})
if decision != netsec.Allow {
    // 拒绝
}
```

也可以一次性完成解析和判定：

```go
decision, err := netsec.CheckAccess(permissionsXML, certSubject, now, req)
```

## 判定语义

- 标准 DDS 文档支持 `<dds><permissions><grant>`，也兼容直接使用
  `<permissions>`；多个 grant 时选择 `subject_name` 与证书主题匹配的 grant。
- `subject_name` 不匹配证书主题时整份文档无效，返回 `ErrSubjectMismatch`。
- `now` 不在 grant 的 `not_before`/`not_after` 闭区间内时整份文档无效。
  过期返回 `ErrExpired`，尚未生效返回 `ErrNotYetValid`；错误同时满足
  `errors.Is(err, ErrInvalidDocument)`。
- `allow_rule`/`deny_rule` 严格按 XML 中出现顺序匹配，首条命中的规则生效。
- 无任何规则命中时始终拒绝。`<default>` 仅为兼容解析，不改变 fail-closed
  行为。
- 规则必须匹配域 ID（`<id>`、`<id_range>`）、主题和分区。`<publish>` 与
  `<subscribe>` 分开匹配，未声明的操作不会命中。
- 主题与分区表达式区分大小写，支持 `*`、`?`、`[seq]`、`[!seq]`、区间和
  POSIX 字符类；`*` 可匹配 `/`，适用于 ROS/DDS 主题名。格式错误的模式不会
  匹配任何内容。
- allow 规则未写分区时只匹配默认空分区；deny 规则未写分区时匹配所有分区。

## 验证

```sh
go test ./...
```

测试覆盖先 deny 后 allow 的首条命中、宽泛 `*` 无法绕过前置 deny、主题大小写
敏感、域 ID/分区/发布订阅匹配、证书主体绑定、过期及尚未生效文档。
