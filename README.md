# vmint — 整数脚本解释器 (Go)

一个字节码 VM，只操作 int64，**无任何宿主调用**。支持局部变量、循环、
函数调用、异常处理（TRY/ENDTRY/THROW），按指令计费，可在任意指令边界
暂停/恢复。

## 指令集

```
func <name> <nlocals>     函数定义（必须有 main）
<label>:                  跳转目标
CONST n / LOAD i / STORE i
ADD SUB MUL DIV MOD NEG   算术（DIV/MOD 除零抛出可捕获异常 -1）
EQ NE LT LE GT GE         比较，压入 0/1
JMP / JZ / JNZ label      跳转（同样计费）
CALL f nargs / RET        函数调用；参数经操作数栈传入局部槽 0..nargs-1
TRY label ... ENDTRY      异常处理器；THROW 抛出（计费）
POP DUP PRINT HALT
```

## 计费与预算

- 每条已执行指令（含跳转、THROW）恰好计 1 gas。
- 预算耗尽产生 VM 级错误 `out of budget`，在指令分发前抛出，
  **不经过脚本 TRY 处理器，无法被 catch 吞掉**（见 `examples/budget.vm`）。
- 脚本 THROW 无人捕获时为首错 `uncaught exception: <v>`，运行终止。

## 暂停 / 恢复

`VM.Snapshot()` 把完整状态序列化为 JSON:`pc`、调用栈（每帧含函数号、
返回地址、**全部局部变量**）、操作数栈、异常处理器栈、已用 gas、预算、
输出、首错。`RestoreVM` 在新进程也能精确续跑。

## CLI

```
go build -o vm .
./vm run        -prog examples/fib.vm                 # 一次执行
./vm checkpoint -prog examples/fib.vm -steps 500 -out snap.json
./vm resume     -snap snap.json                       # 断点续执行
./vm verify     -prog examples/fib.vm [-budget 500]   # 一致性校验
```

`verify` 对**每一个可能的断点** k(0..总步数）分别执行「跑 k 步 → 快照
→ 恢复 → 跑完」，外加一次多断点链式恢复，与一次执行对比
**输出、首错、总费用**三元组，全部一致才算通过。

## 测试

```
go test ./...
```

覆盖：循环求和、递归 fib、跨帧异常捕获、除零捕获、未捕获异常首错、
预算耗尽不可捕获、预算精确边界、全部示例程序的全断点续执行一致性。
