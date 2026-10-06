# configeval

Go 配置求值库：变量可引用其他变量与**显式环境快照**，并对求值结果做增量缓存。

## 核心保证

- **不读进程环境**：求值器从不调用 `os.Getenv`（源码不导入 `os`），所有环境值只来自
  `New` / `SetEnv` 提供的快照（快照会被拷贝，之后外部修改不影响求值）。
- **引用链错误**：缺失变量/环境键（`MissingError`）、循环引用（`CycleError`）、
  类型错误（`TypeError`）都返回 `*RefError`，`Chain` 记录从请求变量到失败节点的
  完整引用链，如 `[var:dsn, var:host, env:DB_HOST]`。
- **依赖摘要**：成功的 `Result` 携带 `Deps`（全部传递依赖及其取值指纹，按引用排序）
  和 `Digest`（对依赖集合的 sha256）。任一依赖变化，摘要必变；输入相同，摘要必同。

## 表达式

```go
Str("lit")            // 字符串字面量
Int(3)                // 整数字面量
Ref("other")          // 引用其他变量
Env("DB_HOST")        // 引用环境快照中的键
Concat(a, b, ...)     // 字符串拼接（非字符串操作数 → TypeError）
Add(a, b, ...)        // 整数求和（非整数操作数 → TypeError）
```

## 缓存、增量重算与冻结边界

- 每个缓存条目记录其**直接依赖的取值指纹**。访问时逐层校验：环境键对比当前快照值，
  变量依赖递归重取并对比摘要。因此 `SetEnv` 后只有传递依赖发生变化的变量会被重算，
  其余直接命中缓存（可用 `Stats()` 观察 computed/reused 计数）。
- `Freeze()` 切开一条边界：递增代际号使全部缓存失效，**任何缓存值都不会越过冻结
  边界**被复用——即使快照完全未变也会重算；边界之后的新快照值绝不会被旧缓存污染。

```go
e := configeval.New(defs, map[string]string{"DB_HOST": "localhost"})
res, _ := e.Get("dsn")
e.SetEnv(map[string]string{"DB_HOST": "db.internal"}) // 仅重算受影响变量
e.Freeze()                                            // 缓存全部作废
```

## 测试

```sh
GO111MODULE=on go test ./...
```

覆盖：成功值与确定性摘要、三类错误的引用链、进程环境隔离（`os.Setenv` 的值不会
泄漏进求值）、环境变化后的增量重算（含传递失效与无关变量保持缓存）、冻结边界、
快照中删除环境键后的缓存失效。
