# fmtlocal — 局部表达式格式化工具

只重写选中的语法节点及其内部空白，绝不触碰节点外的任何字节。

## 语言

四则运算表达式与调用：`+ - * /`、括号、数字、标识符、`f(a, b)`，
支持 `//` 与 `/* */` 注释。输入文件整体是一个表达式。

## 用法

```sh
go build -o fmtlocal .
fmtlocal -file input.txt -start 3 -end 6 [-diff]
# 或从 stdin 读取：
printf 'f(12+34,  5 )' | fmtlocal -start 3 -end 6
```

- `-start`/`-end`：字节偏移选区（end 排除）；`-end -1` 表示光标。
- 输出 JSON：`patch`（字节区间 + 替换文本）、`rationale`（扩区理由）、`checks`（三项验证）。
- `-diff` 额外打印 unified diff。

## 选区扩展规则

1. 选区落在 token 中间时，先扩到完整 token 边界；
2. 再扩到能完整包含选区的最小语法节点（Number/Ident/BinaryExpr/UnaryExpr/CallExpr/ParenExpr）；
3. 每一步扩展都写入 `rationale`。

## 不变式（每次运行自动验证，失败则非零退出）

- `outside_unchanged`：补丁之外的字节逐一相同；
- `idempotent`：对替换结果再次格式化无任何改动（渲染是 token 流的纯函数）；
- `semantics_preserved`：整文件的 token 流（含注释、忽略空白）前后一致，
  因此夹在运算符两侧的注释不会改变语义。

## 测试

```sh
go test ./...
```
