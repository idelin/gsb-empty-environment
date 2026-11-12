# 特权环境变量闭包

特权启动服务需要保留少量环境变量，但变量间引用可能把不可信值重新带入路径。给Go启动宿主写可导入的环境构造包，引用语法仅为${NAME}，按依赖图展开，循环和未定义引用拒绝；敏感变量的每个最终字节必须来自受信源，转义只产生字面字符。用多层间接引用、空值和共享子式验证来源证明与展开预算。

## 包：`netsec`（module `example.org/netsec/q0275`）

### 用法

```go
b := netsec.NewBuilder()
_ = b.SetBudget(netsec.Budget{MaxOutputBytes: 4096, MaxDepth: 8})

// 仅保留白名单中的宿主变量；继承值一律标记为不可信，且永不二次解析。
_ = b.Import(os.Environ(), []string{"USER"})

_ = b.Define(netsec.Spec{Name: "HOME", Template: "/home/${USER}"})
_ = b.Define(netsec.Spec{Name: "TOKEN", Template: "rotated", Sensitive: true})

env, err := b.Build()
if err != nil { /* 未定义/环/敏感污染/预算 */ }
cmd.Env = env.Env()
```

### 安全语义

- 引用语法只有 `${NAME}`；`$$` 是唯一转义，产出且仅产出一个字面 `$`；任何其它裸 `$` 都是语法错误。
- 继承自宿主环境的值是不透明字面量，其中的 `${...}` 文本不会被解释，无法借引用重新进入路径。
- `Build` 先做依赖图校验：未定义引用、引用环在产生任何输出前拒绝（带环路径的类型化错误）。
- 每个输出字节都带 `Origin{Source, Name}` 来源证明；`Value.OriginAt(i)` 可逐字节查询，`Value.AllTrusted()` 汇总。
- `Sensitive` 变量的最终值只要含任一不可信字节即 `SensitiveError`，包括经多层间接引用传播的污染；空值视为真空可信。

### 展开预算

- `MaxDepth`：依赖图最长引用链（按变量节点数），独立于定义顺序与记忆化，在图层面预算。
- `MaxOutputBytes`：累计追加字节数；共享子式只计算一次（记忆化），但每次被引用复制都计入预算，防止重复引用放大。
- `Stats` 暴露 `Expansions`、`CacheHits`、`MaxDepthObserved`、`OutputBytes` 以便验证。

### 验证

`go test -race -cover ./...` 覆盖多层间接引用、空值、共享子式记忆化、逐字节来源、深度与字节预算及全部错误路径。
