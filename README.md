# 有界信道溢出认证

安全设备队列满时会丢弃消息，不能让半次认证被当作完成。实现供Go协议验证宿主调用的模型包，端点状态机与信道容量由输入给出，溢出策略明确为丢新或丢旧，终态声明绑定消息序号；检查是否存在未收到必要消息却进入成功态的轨迹。状态预算耗尽返回未知，用两种溢出策略的小模型比较。

## 包结构（`package netsec`）

- `types.go`：输入模型（`Spec`/`Endpoint`/`Channel`/`Transition`）、溢出策略
  （`DropNew` 丢新、`DropOld` 丢旧）、终态绑定 `SuccessBinding`、事件与
  `Report`/`Budget`/`Verdict`。
- `engine.go`：全局状态、信道 FIFO 队列、序号分配与规范化状态键。
- `apply.go`：单步迁移，队列满时按策略丢弃，并在进入成功态时校验绑定。
- `search.go` / `validate.go`：DFS 状态空间搜索与输入校验。
- `models.go`：三个小模型（正确握手、严格序号握手、半认证缺陷握手）。

## 语义要点

- 每次发送在入队前分配单调序号；无论丢新还是丢旧，被丢弃的发送都消耗序号。
- 丢新：新消息不进队列，已排队消息不受影响。
- 丢旧：队头被驱逐，接收端记录被驱逐序号；`RequireNoGap` 绑定据此检测空洞。
- 终态声明 `SuccessBinding` 列出必需消息标签，可选 `RequireNoGap` 要求无序号
  空洞；缺标签或出现被驱逐空洞即判定半认证，返回 `Unsafe` 与可重放轨迹。
- 成功态是终止态；搜索命中 `MaxStates` 或 `MaxSteps` 一律返回 `Unknown`，
  绝不提前宣称 `Safe`。

## 小模型对比（容量 1 信道）

- `CorrectHandshake`：无重传，两种策略均 `Safe`，且零丢弃。
- `StrictOrderHandshake`：一次有界重传，丢新 `Safe`，丢旧 `Unsafe`
  （重传驱逐原 M1，R 在缺序号 1 的情况下进入 `R_DONE`）。
- `HalfAuthHandshake`：状态机本身把单条 M1 当作完成，两种策略均 `Unsafe`，
  与队列策略无关。

运行：`go test ./... -v`。
