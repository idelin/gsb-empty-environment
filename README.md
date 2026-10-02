# attrgram — 带属性文法（Attribute Grammar）求值器

纯 Go 标准库实现。分两层：

- **`attr`（通用内核）**：属性实例用 `Key{Node, Name}` 标识，每个属性由一条声明了
  显式依赖的 `Rule` 计算。求值器收集全部规则后构建属性依赖图，用 Kahn 算法做
  **拓扑排序** 后按序求值；无法排完的剩余属性即构成 **循环依赖**，以
  `*CycleError` 报告所有成环属性。根节点的继承属性（如空符号表）通过 `Set` 注入。
- **`mini`（示例语言）**：在 `attr` 内核上定义小型变量声明/表达式语言的语义规则：
  - 继承属性 `env`：符号表（变量名 → 类型）自顶向下逐语句传递；
  - 综合属性 `type`：表达式类型自底向上推导；
  - 综合属性 `envOut`：语句执行后的符号表（声明会扩展环境）。
  类型不匹配、未定义变量、重复声明均以 `行:列` 精确报告 AST 节点位置。

## 语言示例

```
var x int = 1 + 2     // 显式声明类型，检查初始化表达式
var y = x * 3         // 类型推导：y : int
var f float = y       // int 可拓宽为 float
var s = "a" + "b"     // 字符串拼接
var b = x < y && !false
print(s)
```

## 运行样例

```
$ go run .
=== diagnostics (with AST node positions) ===
  8:13: cannot apply operator "+" to int and string
  9:9: undefined variable "missing"
  10:1: cannot use int value as string in declaration of "q"
=== final symbol table (synthesized envOut) ===
  {bad: <error>, f: float, ok: bool, q: string, s: string, u: <error>, x: int, y: int}
```

## 测试

```
$ go test ./...
ok  	attrgram/attr	6 个用例：拓扑排序求值、继承/综合链、循环依赖检测、
     	未定义依赖、重复规则
ok  	attrgram/mini	11 个用例：类型推导、env 自顶向下可见性、二元/声明/未定义
     	变量的精确位置诊断、错误不级联、重复声明、运算符类型表、解析错误
```

## 布局

| 文件 | 说明 |
| --- | --- |
| `attr/attr.go` | 属性文法求值内核（依赖图 + 拓扑排序 + 循环检测） |
| `mini/lex.go` / `mini/parse.go` | 词法 / 递归下降语法分析（带行列位置） |
| `mini/ast.go` | AST 节点定义 |
| `mini/rules.go` | 属性文法语义规则（env / type / envOut） |
| `mini/types.go` | 类型系统与不可变符号表 |
| `main.go` | 演示程序 |
