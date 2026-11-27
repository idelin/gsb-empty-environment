# minijit：字节码解释器 + 投机内联去优化 + 两阶段虚拟对象物化

包路径：`prescreen.local/ps-ccd62f6f18`（包名 `minijit`）

## 组件总览

| 文件 | 职责 |
| --- | --- |
| `value.go` | 带标签动态值 `Value`、堆对象 `Object`（隐藏类 + 有序字段）、`StrictEqual` 引用同一性 |
| `bytecode.go` | 操作码、指令 `Instr`、函数 `Function`（常量池/局部槽/异常表）、程序 `Program` |
| `interpreter.go` | 纯字节码解释器：独立物理帧、每帧私有 pc/局部数组/操作数栈、基于 pc 区间的异常逐层展开 |
| `frame_state.go` | 去优化元数据：`Input`（机器槽/虚拟 ID/字面量）、`VirtualObject`、`FrameState` 内联树、`MachineFrame` 快照 |
| `materializer.go` | 两阶段物化器：先全量分配空壳并登记 `virtualID -> heapRef`，再二趟回填字段 |
| `deoptimizer.go` | 按**由外向内**顺序把 `FrameState` 树重建为多层独立解释器帧 |
| `optimizer.go` | 投机 JIT 模型：调用内联进单物理帧、未逃逸对象标量替换进虚拟表、守卫失败即快照去优化并续跑 |
| `graph.go` | DFS 规范化堆图，支持环/共享引用的同构校验 |

## 关键不变量

### 1. 单物理帧 -> 多解释器帧（由外向内）

投机优化把 `main -> f1 -> f2 -> f3` 的多层调用内联进一个 `MachineFrame`。
去优化时 `Deopt` 沿 `FrameState.Inner` 链从最外层开始 `append`，使最内层帧落在
解释器栈顶；每层都恢复独立的字节码 `PC`、局部变量数组与操作数栈。

### 2. 两阶段虚拟对象物化（环安全 + 同一性）

虚拟对象间允许任意共享与循环引用（`A.next=B; B.next=A`），多个内联帧的局部槽
可指向同一个 virtual ID。物化协议：

1. **分配阶段**：为每个 virtual ID 分配一个空堆对象，写入 `refs[id]`，期间不读任何字段，因此不可能因环而递归；
2. **回填阶段**：再次遍历所有虚拟对象，把每个字段的 `Input` 解析为标量、机器槽值或 `refs` 中已登记的堆引用。

重复解析同一 virtual ID 始终返回同一指针，故 `===`（`StrictEqual`）在跨帧、
菱形共享、自环、互环下全部成立。物化是幂等的。

优化层在对象中途逃逸（如参与比较/非内联调用）时使用同一协议的惰性版本，
并保证逃逸后对象获得稳定堆身份、后续变更可见。

### 3. 守卫失败 -> 精准续跑与异常展开

- 整数加法溢出：优化层把两个操作数重新压回栈、pc 回退到该 `Add`，去优化后由解释器重放并提升为浮点；
- 隐藏类守卫失败：恢复对象与期望类入栈、pc 回退到 `GuardClass`（解释器中守卫无副作用）后续跑；
- 去优化后抛异常：重建帧后调用解释器 `throw`，按异常表 `[Start,End)` 从内到外逐层展开，命中则压入异常值并跳到 handler，否则返回 `UncaughtThrowError`。

## 典型用法

```go
prog := buildProgram()

// 纯解释器基线
base, _ := minijit.NewInterpreter(prog).Run("main", args)

// 投机优化 + 去优化
runner := minijit.NewOptimizedRunner(prog)
runner.OnDeopt = func(reason string, frames int) { log.Println(reason, frames) }
opt, _ := runner.Run("main", args)

// 终态值与可达堆图同构
minijit.ResultsEquivalent(base, opt)
```

也可手工构造 `MachineFrame` + `FrameState` 直接调用 `Deopt` 与 `NewMaterializer`，
见 `materializer_test.go`。

## 验收测试映射

| 验收点 | 测试 |
| --- | --- |
| 3 层以上嵌套内联帧去优化 | `TestThreeLayerInlineDeoptAndGraphEquivalence`、`TestDeoptReconstructsIndependentFrames` |
| 循环引用物化防死循环 | `TestTwoPhaseMaterializationCyclesAndIdentity`、`TestThreeLayerInlineDeoptAndGraphEquivalence` |
| 跨帧共享同一虚拟对象身份 | `TestTwoPhaseMaterializationCyclesAndIdentity`（菱形 + 多帧同 ID） |
| 去优化后抛异常逐层捕获 | `TestDeoptThenThrowCaughtAcrossReconstructedFrames`、`TestDeoptThenThrowUncaught` |
| 终态与堆图同构于纯解释器 | `TestInterpreterOptimizedEquivalenceTable`、`TestThreeLayerInlineDeoptAndGraphEquivalence` |
| 隐藏类突变守卫 | `TestHiddenClassGuardDeopt` |

运行：`go test ./... -v`（另通过 `go test -race ./...` 与 `go vet`）。
