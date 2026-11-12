# P4表项头空间冲突检测

Go 控制器用的 P4Runtime 表项授权与冲突检测包。所有表项写入先经过头空间和端口检查，再对同表项做优先级歧义检查；写入以控制器版本做乐观并发控制，整批操作要么全部生效，要么全部回滚。

## 安全规则

- 三态（ternary）、LPM、exact 和 wildcard 匹配都按位展开为头空间立方体；每个字段的匹配集合必须完全包含在租户授权的模式并集内。三态掩码扩大、LPM 前缀变短、跨到其他租户地址都会返回 `ErrUnauthorizedMatch`。
- 同一张表中，两个表项的头空间交集非空、优先级相同、动作不同时返回 `ErrPriorityConflict`。动作相同或优先级不同则不视为歧义。
- `Action.Ports` 中每个出端口都必须属于提交租户，否则返回 `ErrUnauthorizedPort`。
- `Apply` 先校验 `ExpectedVersion`。批次内任一操作失败时，表项和版本均不变；成功后版本恰好加一。

头空间用 `Pattern{Value, Mask}` 表示，掩码为 1 的位是固定位。一个字段可以配置多个授权模式，它们按并集处理，因此相邻前缀也可以共同覆盖一个更大的表项范围。

## 基本用法

```go
authorized, _ := netsec.NewPrefixPattern([]byte{10, 20, 0, 0}, 16)
ctrl, err := netsec.NewController([]netsec.Tenant{{
    ID:          "tenant-a",
    HeaderSpace: netsec.HeaderSpace{"ipv4.dst": {authorized}},
    Ports:       []uint32{10, 11},
}})
if err != nil {
    return err
}

entry := netsec.TableEntry{
    Tenant:   "tenant-a",
    Table:    "acl",
    ID:       1,
    Priority: 100,
    Matches: []netsec.FieldMatch{{
        Field: "ipv4.dst",
        Kind:  netsec.MatchLPM,
        Value: []byte{10, 20, 0, 0},
        Mask:  []byte{0xff, 0xff, 0x00, 0x00},
    }},
    Action: netsec.Action{Kind: "forward", Ports: []uint32{10}},
}

version, err := ctrl.Insert(entry, ctrl.Version())
```

批量原子写入：

```go
next, err := ctrl.Apply(netsec.WriteRequest{
    ExpectedVersion: version,
    Operations: []netsec.Operation{
        netsec.ModifyOperation(modifiedEntry),
        netsec.DeleteOperation("acl", oldEntryID),
    },
})
```

并发写使用同一个 `ExpectedVersion` 时只有一个批次能提交，其余返回 `ErrVersionConflict`。

## 验证

```sh
go test ./...
go test -race ./...
```

测试覆盖掩码越界、相邻授权前缀并集、相同优先级歧义、多字段交集、出端口越权、版本化修改/删除、批次回滚及并发 CAS 写入。
