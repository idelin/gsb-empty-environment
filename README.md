# docexec — 文档示例执行器

按依赖顺序执行文档中的 ` ```run ` 代码块，块间共享命名空间，
每条输出都映射回原文行号。解释器为纯本地实现，不调用任何外部命令。

## 文档格式

    ```run id=fetch needs=prepare defines=URL,DATA
    set URL https://example.invalid
    emit fetched $URL
    ```

- `id`：块标识（缺省为 `block-N`）
- `needs`：逗号分隔的前置块 id，全部成功后才运行本块
- `defines`：本块声明定义的变量名，用于静态重名检查

普通代码围栏（非 `run`）会被忽略。

## 指令集（纯本地解释）

| 指令 | 语义 |
| --- | --- |
| `set NAME value...` | 定义变量，值中可做 `$VAR` / `${VAR}` 展开；重复定义即失败 |
| `emit text...` | 展开变量后输出一行，记录其原文行号 |
| `expect NAME value...` | 断言变量值，不等则块失败 |
| `fail text...` | 显式让块失败 |
| `# ...` / 空行 | 忽略 |

引用未定义变量是错误（不会像 shell 一样静默展开为空）。

## 执行语义

- **依赖排序**：Kahn 拓扑排序，同层按文档顺序，结果确定。
- **共享命名空间**：所有成功块的 `set` 写入同一命名空间，供后续块引用。
- **事务性**：块在命名空间副本上执行，成功才提交；失败的块不会留下
  任何部分写入（无状态泄漏），其输出也不计入报告。
- **失败隔离**：依赖失败/被跳过的块会被跳过（可传递），并注明原因；
  无关块照常执行。

## 静态验证（执行前）

`Validate` 在执行前一次性报告所有问题：块 id 重复、跨块重名定义
（`defines` 冲突）、`needs` 指向未知块、依赖环（报告具体环路径）。
验证失败则不执行任何块。

## 使用

```sh
go run ./cmd/docexec examples/demo.md
go test ./...
```

报告示例：

    [ok] block "fetch" (lines 9-14)
      line 13 | fetched payload-from-https://example.invalid/data
    [failed] block "broken" (lines 21-25)
      error at line 24: explicit fail: simulated network error
    [skipped] block "dependent" (lines 27-29)
      skipped: dependency not ok: broken (failed)

## 代码结构

- `parser.go` — 文档解析，提取 `run` 块及行号
- `validate.go` — 重名/未知依赖/环检测
- `interp.go` — 指令解释器与变量展开（无外部命令）
- `exec.go` — 拓扑调度、事务执行、失败跳过、报告
- `cmd/docexec/main.go` — CLI
