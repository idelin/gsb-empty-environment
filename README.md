# iscript — 沙箱化整数脚本解释器 (Go)

一个栈式字节码 VM，纯整数运算，**无任何宿主调用能力**（无 IO、无系统接口，
唯一副作用是向输出缓冲追加整数）。

## 特性

- **局部变量**：每个函数帧持有独立的 `locals` 槽位
- **循环**：`jmp` / `jz` 条件跳转
- **函数调用**：`call` / `ret`，递归安全，返回值经操作数栈传递
- **异常处理**：`try <label>` / `untry` / `throw`，运行时故障（除零、栈
  下溢等）同样可被捕获；异常可跨帧传播到调用方的 handler
- **计费**：每条已执行指令计 1；命中的跳转 / call / ret 额外 +1；抛错
  （`throw` 或运行时故障）额外 +1
- **预算**：`budget >= 0` 时费用超支立即以 `CodeBudget(-1)` 失败，该错误
  **不可被 try/catch 捕获**，直接清空调用栈
- **暂停/恢复**：`pause` 指令或 `RunQuantum(n)` 在任意指令边界挂起；
  `Snapshot()` 把**完整动态状态**（调用栈、局部变量、操作数栈、异常
  handler、输出、累计费用、首错、状态）序列化为 JSON，`Restore()` 恢复

## 正确性保证（测试覆盖）

`interp/vm_test.go` 对比一次性执行与任意断点续执行：

- `TestOneShotVsChunkedEquivalence`：20 个随机种子、随机量子切片，每个
  断点都做 JSON 快照+恢复，断言输出、首错、费用、终态完全一致
- `TestSnapshotEveryInstruction`：每条指令执行后都序列化/恢复
- `TestBudgetExhaustionUncatchable`：预算耗尽不被脚本 catch 吞掉

## 用法

```sh
go test ./interp/                          # 全部测试
go run ./cmd/iscript examples/demo.is      # 一次性执行
go run ./cmd/iscript -quantum 3 examples/demo.is   # 分片+快照恢复执行
go run ./cmd/iscript -budget 500 examples/demo.is  # 带预算
```

三种方式对同一脚本的输出、首错与费用完全相同。

## 脚本语法

```
func main 0 1        ; 函数声明：名字 参数数 局部变量数（main 为入口）
label:               ; 跳转标签
push <int>           ; load/store <slot>
add|sub|mul|div|mod|neg
eq|ne|lt|le|gt|ge
jmp|jz|try <label>
call <func>          ; 参数个数由 func 声明自动填充
ret|untry|throw|print|pause|halt
```

## 文件

- `interp/vm.go` — VM：指令集、计费、异常、暂停/恢复、JSON 序列化
- `interp/asm.go` — 文本脚本汇编器（两遍：标签/函数解析）
- `interp/vm_test.go` — 功能与等价性测试
- `cmd/iscript/main.go` — CLI
- `examples/demo.is` — 示例（递归 fib + 循环 + try/catch + pause）
