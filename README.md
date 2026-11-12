# 位置无关代码访问证明

插件验证器要证明所有数据读取都落在声明区域内。写供Go验证宿主调用的仿射地址分析包，基本块操作只含基址加常数、寄存器复制和区间分支，合流保留地址来源集合；外部输入只允许经显式边界检查收窄。循环采用给定不变量并验证保持性，输出每条读取的范围证书，用有限输入解释器核对。

## 包结构（`package netsec`）

| 文件 | 职责 |
| --- | --- |
| `ir.go` | 受限 IR：区域、输入、操作（`OpInput/OpBase/OpAddConst/OpCopy/OpLoad`）、区间分支终结符 |
| `interval.go` | 带溢出保护的整数闭区间（交集、包络、平移、加法、包含/偏序） |
| `affine.go` | 仿射抽象值：偏移区间 + 区域来源集合，合流取来源并集 |
| `frame.go` | 程序点寄存器环境及其合流、偏序 |
| `analyze.go` / `transfer.go` / `certify.go` | 工作列表分析、传递函数、读取范围证书 |
| `validate.go` | IR 结构与声明一致性校验 |
| `interpreter.go` | 带来源标记的有限输入具体解释器 |
| `cross.go` | 输入枚举与静态证书的双向交叉核对 |

## 核心保证

- **来源不丢失**：`Affine.Origins` 记录每个值可能派生自哪些区域基址；`Join`
  对来源取并集、对同源偏移取区间包络，任意控制流合流后仍可追溯全部来源。
- **外部输入显式收窄**：`OpInput` 引入的值是无界顶，只有 `TermBranch` 的
  显式比较能收窄它；未收窄的外部值参与 `OpLoad` 寻址直接产生 `Violation`。
- **循环靠不变量**：在 `Program.Invariants` 中声明循环头不变量，分析器把它
  作为归纳假设，并对每条入边（前置边、回边）做 `frameLeq` 包含性核对。
- **每条读取一张证书**：`Certificate` 给出来源集合、逐来源绝对访问区间、
  统一界 `[AccessLo, AccessHi]` 与收窄所用的边界检查站点。
- **有限输入核对**：`EnumerateInputs` 枚举声明输入域的笛卡尔积，`Run`
  具体执行，`CrossCheck` 验证「已认证站点必在界内」，并区分确认越界与
  保守未触发。

## 宿主调用

```go
rep, cross, err := netsec.Validate(prog)
if err != nil { /* IR 非法 */ }
if !rep.OK { /* 存在无法证明的读取或失败的不变量：查看 rep.Violations */ }
if !cross.OK { /* 静态证书与具体执行不一致（内部可靠性问题） */ }
for _, c := range rep.Certificates {
    // c.Origins / c.OriginRanges / c.AccessLo..AccessHi
}
```

仅做静态分析用 `netsec.Analyze(prog)`；单独跑具体执行用 `netsec.Run`。
测试见 `*_test.go`，覆盖线性访问、合流来源并集、边界检查收窄、循环不变量
保持/破坏、常量越界与解释器交叉核对。
