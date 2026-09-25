# Rill：带结构记录与读/写方差的小语言

Rill 是一个从零实现的小型过程式语言，核心特性是**结构记录类型**（structural
records）、**记录宽度子类型**，以及对**只读 / 可写位置方差**的显式区分。
编译分为手写词法分析、递归下降语法分析、语义检查三步，再由手写的树遍历
解释器执行。不使用 `eval`、`exec` 或任何现成编译器/解析器生成器，仅依赖
Python 3.11 标准库与 `unittest`。

## 运行与测试

```sh
python3.11 -m rill examples/demo.rec        # 运行样例
python3.11 -m unittest discover -s tests    # 测试套件
```

## 语法速览

```
fn name(readonly  r: {name: Str}) { ... }    # 只读参数（读位置，宽度协变）
fn bump(writable  r: {n: Int})    { ... }    # 可写参数（写位置，必须精确匹配）

let p = {name: "Ada"}        # 不可变绑定，字段也冻结
var q = {n: 1}               # 可变绑定
q.n = q.n + 1                # 更新已存在字段
extend q.tag = "hot"         # 显式扩展：增加不存在的字段（流敏感地加宽类型）
print(q.n)
```

支持的类型：`Int`、`Str`、`Bool`、`Unit` 与匿名结构记录 `{字段: 类型, ...}`；
语句包括 `let` / `var`、赋值、`if` / `else`、`while`、`return`、块和
`print(...)`；表达式支持算术、比较、逻辑、字符串拼接与函数调用。

## 类型规则（重点）

- **记录宽度子类型，只读位置协变**：记录类型字段顺序无关；`{name: Str, age: Int}`
  是 `{name: Str}` 的子类型。传给 `readonly` 少字段参数时，额外字段允许存在，
  嵌套记录在只读位置也按宽度协变判定。
- **可写位置不变（拒绝不安全协变）**：`writable` 参数会经别名修改记录，实参
  类型必须与形参逐字段精确匹配。窄记录传入宽形参时，函数体可能写入实参并不
  存在的字段，因此编译期拒绝；宽记录传入窄形参同样不做隐式放宽。
- **缺字段访问编译期拒绝**：`r.missing` 在静态类型中找不到该字段即报错，
  错误指向字段名的 UTF-8 字节区间。
- **更新不存在字段必须显式扩展**：`p.age = 36` 中 `age` 不存在会被拒绝；
  必须写 `extend p.age = 36`，检查器随即在当前流中把 `p` 的类型加宽为
  `{name: Str, age: Int}`（分支内的加宽不会泄漏到分支外）。
- `let` 绑定与其可达记录整体只读；向 `readonly` 参数写字段同样被拒绝。

## 源码位置

所有词法 token 与 AST 节点都带半开区间 `[start, end)`，按 **UTF-8 字节**
（而非字符或列）定位；中文标识符（如 `人.名`）可正确处理。错误输出包含
文件、行、字节列、源代码行与 `^` 指示。

## 被拒绝的样例（均为编译错误）

- `examples/errors_missing_field.rec`：访问不存在的字段。
- `examples/errors_unsafe_covariance.rec`：把宽记录传给要求精确匹配的
  `writable` 参数。
- `examples/errors_update_missing.rec`：对不存在的字段赋值而未用 `extend`。
- `examples/errors_readonly_write.rec`：通过只读位置写记录字段。
