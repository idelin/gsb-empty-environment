# netsec：Go 缓存解码白名单组件

`example.org/netsec/q0486` 提供一个可导入的类型白名单解码器，用于处理不可信的自描述缓存数据。解码器不会根据载荷中的类型标识调用 `encoding/json` 的任意类型反序列化；它只按注册的 `(类型标识, 版本)` 构造显式允许的具体 Go 类型，并只填充显式允许的字段。

## 载荷格式

```json
{
  "type": "cache.entry",
  "version": 1,
  "data": {
    "key": "user:42",
    "value": "{\"name\":\"alice\"}",
    "created_at": 1700000000,
    "expires_at": 1700003600,
    "metadata": {
      "namespace": "users",
      "owner": "cache-service",
      "revision": 3
    },
    "tags": ["profile", "v1"]
  }
}
```

- `type` 必须与注册的类型标识逐字节、大小写敏感匹配。
- `version` 必须与注册版本完全相等；缺失、非整数或不同版本都会被拒绝。
- `data` 必须为对象，且只能包含该类型 `FieldSpec` 中列出的字段。
- 重复 JSON 键、`null` 字段、未知字段、大小写变体字段都会被拒绝。

## 使用

```go
registry := netsec.NewDefaultRegistry()

entry, err := netsec.DecodeAs[netsec.CacheEntry](registry, payload)
if err != nil {
    return err
}
```

自定义缓存类型：

```go
type Session struct {
    ID     string `json:"id"`
    UserID int64  `json:"user_id"`
}

registry := netsec.NewRegistry(
    netsec.WithMaxDepth(16),
    netsec.WithMaxBytes(1<<20),
)

err := netsec.RegisterType[Session](
    registry,
    "cache.session",
    1,
    []netsec.FieldSpec{
        {Name: "id", Required: true, LenRange: &netsec.LenRange{Min: 1, Max: 128}},
        {Name: "user_id", Required: true, IntRange: &netsec.IntRange{Min: 1, Max: 1<<62}},
    },
    func(session *Session) error {
        if session.ID == "" {
            return fmt.Errorf("id is empty")
        }
        return nil
    },
)

value, err := registry.Decode(payload)
session, ok := value.(*Session)
```

`RegisterType[T]` 保证工厂只返回新的 `*T`。也可以使用底层 `Register(TypeSpec)` 注册类型。

## 安全边界

- 只注册具体的结构体类型和字段；不注册接口、`map`、函数、通道或自定义 `UnmarshalJSON` 字段。
- 嵌套结构体字段也必须作为独立类型注册，否则解码失败。
- 解码器逐字段构造值，不调用目标类型的 `UnmarshalJSON`/`TextUnmarshaler`。
- 默认最大嵌套深度为 32，最大载荷为 4 MiB；可用 `WithMaxDepth`/`WithMaxBytes` 收紧。
- 解码后执行字段整数范围、字符串/切片长度以及 `TypeSpec.Validate` 跨字段一致性校验。
- `$ref` 引用只允许指向 `#/data/...`；循环引用、越界指针和悬空指针都会被拒绝。
- 引用校验带缓存，避免大量重复引用造成遍历放大。

`$ref` 示例（非循环共享引用）：

```json
{
  "type": "test.node",
  "version": 1,
  "data": {
    "name": "root",
    "next": {"$ref": "#/data/alt"},
    "alt": {"name": "shared"}
  }
}
```

## 内置类型

- `cache.entry` v1：`CacheEntry`，包含 `key`、`value`、`created_at`、`expires_at`、`metadata`、`tags`。
- `cache.metadata` v1：`CacheMetadata`，包含 `namespace`、`owner`、`revision`。

内置校验包括键/标签长度、TTL 范围、时间先后关系、命名空间控制和重复标签检查。

## 验证

```sh
go test ./... -race
go vet ./...
```

测试覆盖合法往返、类型混淆、未知类型、版本变化、重复/未知字段、过深嵌套、循环引用、悬空引用、范围与一致性校验，以及拒绝路径不触发类型工厂构造。
