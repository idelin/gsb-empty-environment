# rule-ast-interpreter

面向多租户规则服务的沙箱化表达式解释器。只接受纯数据 AST（JSON 可序列化），
不含 `eval`、宿主函数调用或动态模块导入——AST 中没有任何可执行代码，解释器
是唯一求值入口。

## 支持的节点

| kind | 字段 | 语义 |
| --- | --- | --- |
| `int` | `value: number \| string \| bigint` | 有符号 64 位整数字面量，越界报 `overflow` |
| `bool` | `value: boolean` | 布尔字面量 |
| `var` | `name` | 变量引用，未定义报 `unbound-var` |
| `neg` / `not` | `arg` | 一元取负 / 逻辑非 |
| `arith` | `op: add\|sub\|mul\|div\|mod`, `args: [a, b]` | int64 运算，溢出报 `overflow`，除零报 `domain` |
| `cmp` | `op: eq\|ne\|lt\|le\|gt\|ge`, `args: [a, b]` | 比较；`eq/ne` 要求同类型且非数组 |
| `logic` | `op: and\|or`, `args: [a, b]` | 短路逻辑，未执行分支不计步、不求值 |
| `if` | `cond, then, else` | 只访问被选中分支 |
| `map` | `array, param, body` | 结果数组按元素数预占空间 |
| `filter` | `array, param, body` | 每保留一个元素占 1 单位空间 |
| `fold` | `array, param, acc, init, body` | 不产生新数组 |

## 预算与隔离

- 每访问一个 AST 节点计 1 步；map/filter/fold 每读一个数组元素额外计 1 步；
  创建数组按元素数计空间。所有预算检查发生在动作之前。
- 每个请求有独立的步数/空间计数与取消标记；`cancel()` 只影响本请求。
- 同租户所有在途请求共享 `tenantSpaceLimit` 空间限额；请求无论成功、失败
  还是被取消，结束时都会释放其预留（`finally` 中释放）。
- 求值使用显式堆栈（生成器蹦床），深度任意不撑爆调用栈；每 `chunkSize`
  次操作让出一次事件循环（`setImmediate`），多租户请求公平交错。
- 错误分类：`overflow` / `type` / `unbound-var` / `domain` / `budget-steps` /
  `budget-space` / `tenant-space` / `cancelled`，均携带 AST 路径（如 `$.args[1]`、
  `$.else`、`$.body[0]`）。

## 运行

需要 Node >= 23.6（原生 TypeScript type-stripping）：

```sh
node --test test/interpreter.test.ts
```
