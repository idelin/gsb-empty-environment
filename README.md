# flowsec — 脚本 IR 信息流分析库（Go）

对结构化脚本 IR 做显式有限格上的信息流分析：赋值合并数据标签与控制依赖（pc）标签，
分支汇合后 **不自动丢弃 pc 依赖**；只有带授权的显式 `Declassify` 节点才能缩小标签，
并把降密来源记入标签的 provenance。对每个 sink 输出允许/拒绝判定、拒绝原因和最短传播链。

## 模型

- **格**：`NewLattice(names, covers)` 由覆盖关系（Hasse 图）显式构造有限格，
  预计算并校验 join/meet 表；非格结构直接报错。`ChainLattice` 是全序便捷构造。
- **标签**：`Label{Level, Receivers, Prov}`。`Receivers` 为允许接收者集合（`nil` = 不受限）。
  合并时等级取 join、接收者集合取交、provenance 取并。
- **IR**：`Input / Const / Assign / If / Declassify / Sink`，结构化无跳转；
  循环需由前端先展开或抽象（本库不处理）。
- **策略**：`Policy.Grants[instrID] = Grant{Token, ToLevel, AddReceivers}`。
  降密必须：节点有授权、token 匹配、目标等级与授权一致、是**缩小**（`ToLevel ≤ 当前`）、
  接收者不超出授权。任一不满足则拒绝、标签不变、记录 violation。

## 语义要点

- `Assign`：目标标签 = 各源标签 join 当前 pc 标签。
- `If`：条件标签压入 pc 栈执行两个分支，汇合时逐变量 join。
  pc 影响已烘焙进分支内赋值的标签，**汇合后依然保留**（隐式流不会在 join 点被洗掉），
  只能经显式授权降密消除。
- `Sink`：允许 ⟺ `label.Level ≤ MaxLevel` 且 `receiver ∈ Receivers`。
  拒绝时给出原因，并给出通往最近"肇事"输入源的最短传播链（BFS，边角色为
  `data / pc / merge / declass`）。

## 双运行对照

`Analyze`（格 join 语义）与 `AnalyzeSets`（独立的 may-flow 集合语义，按输入源追踪影响集）
共享的仅有 IR/Policy 类型定义。`CrossValidate` 比较两者对每个 sink 的判定，
分歧会被报告而不是被默默吞掉。注意这是**同一程序上两次独立观察的经验性对照**，
不是正确性证明。

## 运行

```sh
GO111MODULE=on go test ./...
GO111MODULE=on go run ./cmd/flowdemo
```

demo 覆盖：直接泄漏（拒绝）、经分支条件的隐式泄漏（join 后仍拒绝）、
授权降密（允许且记录 provenance）、伪造 token 降密（拒绝并记 violation），
并做双运行对照。

## 限制（重要）

本库**不声称完整检测所有非干扰性违规**。已知边界：

- 不建模终止通道、定时/资源通道、异常控制流；
- 无别名/指针/堆分析，无过程间调用（IR 需预先内联）；
- 循环不在 IR 内处理，前端展开方式影响结果；
- 集合语义与格语义在"降密后再抬升"等程序上可能合理分歧，对照仅用于暴露分歧；
- 结果是本 IR 本策略下的保守判定，不构成对任意脚本非干扰性的完备判定。
