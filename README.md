解释器团队要在合入优化前发现越界读被分支预测隐藏的泄漏，请交付供审查服务调用的Go分析包和可重放见证。本题使用自定义微型指令集，不宣称模拟真实CPU：8个8位寄存器、256字节内存、64条以内指令，支持常量、模256加法、异或、load、probe、条件跳转、barrier和halt；load地址取指定寄存器的8位值，读取单字节，条件跳转比较两寄存器是否相等。load只改寄存器，probe记录其地址除16后的整数；跳转可按实际条件执行，或仅在全程一次选择相反分支并推测执行最多8步，之后回滚寄存器和内存但保留probe记录；barrier立即结束推测；推测中的其他条件跳转按实际条件执行，halt也结束推测，回滚后从原分支正确后继继续。输入指定至多2个公开输入字节及1个秘密字节的位置，公开字节与秘密字节位置互不重叠且由输入指定其位于寄存器或内存，其他初值固定，每次运行最多64步，超限为未知。找两个公开输入相同、秘密不同且架构执行的probe记录相同、推测记录不同的运行，返回输入、误预测位置及逐步记录；没有见证只有在穷尽给定预算内全部候选后才报有界未发现，否则报未知。可接受穷举，但以调用方的执行次数预算限流。测试必须同时覆盖存在泄漏的越界查表、插入barrier的修复、直接公开泄密不计本题漏洞，以及回滚后秘密仍不能进入架构寄存器。

## 交付物

包 `leaklab`（module `prescreen.local/ps-65e4b96950`），纯标准库、无外部依赖：

- `isa.go`：指令集、操作码、汇编解析（支持标签/注释）与 `AsmBuilder`。
- `machine.go`：机器状态、架构执行、单次误预测推测窗口（≤8 步）、回滚与逐步记录。
- `analyzer.go`：预算限流的确定性穷举、候选分组、见证判定与三种结论状态。
- `witness.go`：见证 JSON 编解码与独立重放校验（逐字节核对寄存器/内存/逐步记录）。
- `programs.go`：内置场景（越界查表泄漏、barrier 修复、直接公开泄密、寄存器槽变体）。
- `cmd/leaklab`：`demo` 生成见证、`verify` 重放校验的命令行入口。

## 机器语义约定

- 8 个 8 位寄存器 `r0..r7`，256 字节内存，所有地址/运算按 8 位回绕（加法即模 256）。
- `probe ra` 记录 `{set: ra/16, addr: ra}`，区分架构轨迹 `ArchTrace` 与推测轨迹 `SpecTrace`。
- 每次运行至多一次误预测：仅 `RunOptions.MispredictPC` 指向的 `jmpeq` 实际退休时反转；
  推测窗口内其他 `jmpeq` 按真实条件执行；窗口在 barrier/halt、满 8 步或推测 PC 越界时结束。
- 窗口结束回滚寄存器和内存，probe 与逐步记录保留；架构从原分支正确后继继续。
- 架构退休步数超过 64 记为 `step-budget`（未知运行，不参与配对）。

## 分析结论

- `found`：找到公开输入相同、秘密不同、同一误预测站点、架构 probe 序列一致而推测
  probe 序列不同（两侧推测轨迹均非空）的一对运行，返回完整 `Witness`。
- `bounded-not-found`：预算足以穷尽全部候选（公开组合 × 256 个秘密 × 各跳转站点）仍无见证。
- `unknown`：预算在穷尽前耗尽，或存在超 64 步的运行；不得据此断言无泄漏。

预算单位是模拟器执行次数（`AnalysisResult.Executions`），每次调用前检查，绝不超支。
枚举顺序固定（公开组合小端字典序、秘密 0..255、跳转站点按 PC 升序），结果可重放。

## 命令行

```sh
go run -buildvcs=false ./cmd/leaklab demo --scenario leaky --out witness.json
go run -buildvcs=false ./cmd/leaklab demo --scenario barrier
go run -buildvcs=false ./cmd/leaklab demo --scenario public
go run -buildvcs=false ./cmd/leaklab verify --in witness.json
```

## 测试

`go test ./...` 覆盖题面要求的四类断言（见 `analyzer_test.go`）：

- 存在泄漏的越界查表：必须产出可重放见证。
- 插入 barrier 的修复：穷尽候选后 `bounded-not-found`。
- 直接公开泄密：推测轨迹不随秘密变化，不计本题漏洞。
- 回滚后秘密不能进入架构寄存器：256 个秘密下架构寄存器/非输入内存逐字节不变。

另含机器语义（模 256、probe 桶、步数预算、单次误预测、8 步窗口、barrier/halt 终止）
与汇编解析/校验的单元测试，并通过 `go test -race ./...`。
