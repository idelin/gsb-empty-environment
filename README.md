# 指针认证域隔离

安全运行时给指针附认证标签，认证上下文复用可能让数据指针被当函数指针。写供Go运行时调用的指针封装包，标签由注入MAC接口覆盖地址、对象代次、指针种类和调用上下文，派生指针必须重新认证且仅允许保留或收窄种类；地址重用废弃旧代。用真实HMAC测试跨对象、跨上下文交换及验证后对象退役的使用门禁。

## 实现

`netsec` 包（无第三方依赖，仅用标准库）：

- `authptr.go`：`MAC` 注入接口、`AuthenticatedPointer` 封装、`Registry`
  代次/退役注册表与使用门禁。
- `hmac.go`：基于 `crypto/hmac` 的真实 MAC 实现，比较走常量时间的
  `hmac.Equal`。
- `authptr_test.go`：HMAC-SHA256（含 RFC 4231 已知答案向量）驱动的
  攻击面测试。

### 安全语义

- 标签覆盖四要素：地址、对象代次、指针种类（数据/函数）、调用上下文
  （安全域 + 调用点），编码带长度前缀以防歧义碰撞。
- 派生指针 `Derive` 重新计算标签；种类按权限等级只能保留或收窄
  （函数 → 数据允许，数据 → 函数拒绝并返回 `ErrWidenedKind`）。
- 地址重新 `Mint` 分配新代次并覆盖映射，旧标签门禁返回
  `ErrStaleGeneration`；`Retire` 后同代次全部指针（含派生指针）返回
  `ErrRetired`。
- `UseForCall` 仅接受函数指针，`UseForDeref` 仅接受数据指针，
  杜绝上下文复用时数据指针被当函数指针调用。

### 用法

```go
tagger, _ := netsec.NewHMACTagger(sha256.New, secretKey)
registry, _ := netsec.NewRegistry(tagger)

fn, _ := registry.Mint(objAddr, netsec.KindFunction, netsec.Context{
    Domain: "runtime.net", CallSite: 101,
})

target, err := registry.UseForCall(fn, currentContext) // 门禁通过才放行地址

inner, err := registry.Derive(fn, objAddr+offset, netsec.KindData, currentContext)
dataAddr, err := registry.UseForDeref(inner, currentContext)

_ = registry.Retire(objAddr) // 验证后退役，同代次指针立即失效
```

### 测试

```sh
go test ./... -race -cover
```

覆盖：跨对象标签交换、跨上下文交换、字段篡改、种类提权、地址重用旧代
失效、退役门禁、不同 MAC 密钥隔离。
