# 对象污点与原型写入

这是可嵌入 Go 服务的原型污染静态分析包。输入是受限脚本 AST，分析在执行前完成，不执行不可信脚本。

## 支持的语言

- 对象分配：`NewExpr`
- 变量赋值与别名：`AssignStmt` + `VarExpr`
- 字符串常量与外部字符串：`StringExpr`、`ExternalStringExpr`
- 字段读写：`FieldExpr`、`AssignStmt`
- 顺序、条件分支：`Program`、`IfStmt`
- 有界循环：`WhileStmt`（最多 16 次）和 `RepeatStmt`（最多 16 次）

`Validate` 强制以下限制：

- 字符串常量只能取 `a`、`b`、`__proto__`、`constructor`、`prototype`
- 外部字符串也只会产生这 5 个值
- 对象分配点最多 32 个
- 循环上界最多 16

## 原型模型

- 所有分配对象默认共享同一个共享原型 `P`
- 任意对象读取 `constructor` 得到固定构造对象 `C`
- `C.prototype` 指回 `P`
- 写 `__proto__` 会改变接收对象的原型；非对象值按无操作处理
- 写普通字段是写接收对象自己的字段，不会修改原型
- 显式写入的 `constructor` / `prototype` 字段按普通字段遮蔽默认值
- 写 `P` 的任何字段都是危险写，包括 `P.__proto__`

因此，`o.a = "b"` 不会报告；而 `o.__proto__.a = "b"`、
`o.constructor.prototype.a = "b"` 以及通过外部字符串键写共享原型都会报告。

## 分析接口

```go
program := netsec.Seq(
    netsec.Let("o", netsec.NewObject()),
    netsec.Let("p", netsec.Get(netsec.Var("o"), netsec.Lit("__proto__"))),
    netsec.SetField(netsec.Var("p"), netsec.External(), netsec.Lit("a")),
)

report, err := netsec.Analyze(program)
if err != nil {
    // 程序不符合受限语言
}
for _, finding := range report.Findings {
    // finding.NodeID、finding.Node、finding.Certainty、finding.Keys
    // finding.Witnesses 包含别名/字段读取/外部字符串的数据流证据
}
```

分析是流敏感、堆敏感的抽象解释：

- 变量和字段值使用有限对象集合与字符串集合传播别名
- 条件分支分别分析后合并；合并造成的不确定性标记为 `MayWrite`
- 循环最多展开到声明的上界；循环内采用弱更新，避免丢失任一具体迭代的事实
- `MustWrite` 表示该写节点每次执行都写共享原型；`MayWrite` 表示至少存在一条路径或一次迭代可能写共享原型

## 具体执行与漏报验证

`Execute` 是与分析器语义一致的具体解释器；调用方需为每个外部字符串节点提供一个固定值后执行程序。
`CheckSoundness` 会穷举全部外部字符串组合，并检查：

**任何具体执行中到达共享原型写的节点，静态报告都必须包含该节点。**

```go
result, err := netsec.CheckSoundness(program, 100000)
if err == nil && len(result.MissedNodes) == 0 {
    // 在该小程序及全部外部输入组合上没有漏报
}
```

`AnalyzerOnlyNodes` 可能包含分支/循环合并导致的保守误报；这些误报在
`Report` 中带有 `MayWrite` 标记。测试同时包含“正常独立对象字段写不得误报”
的直接断言，以及有限程序族和固定种子的随机小程序穷举对照。
