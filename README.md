# HTTP二代流状态合法性

接入代理需拒绝已半关闭方向继续发送的数据。实现供Go帧分派宿主调用的流状态包，区分本地与远端创建、保留、半关闭及关闭，流号按发起方奇偶递增；状态非法的帧不得进入业务回调。用双向事件模型枚举同时关闭、保留流激活、迟到RST和连接代次复用，核对首个错误及资源释放。

## 接口摘要

- `NewConn(role)`：按本端角色（`Client`/`Server`）创建连接级状态机，本地流号奇偶由角色决定，步进为 2。
- `Conn.Dispatch(Event)`：宿主逐帧调用，返回 `Verdict`；仅 `Deliver` 可进入业务回调，`Drop`（迟到 RST）与 `Reject`（状态非法）必须拦截。
- `Event`：双向事件模型（`Send`/`Recv` × HEADERS/DATA/RST_STREAM/PUSH_PROMISE），携带连接代次 `Generation` 与 `EndStream` 标志。
- `Conn.Reset()`：连接代次 +1 并释放全部流资源（存活流与关闭墓碑），旧代次帧以 `ErrStaleGeneration` 拒绝，流号可从初始值复用。
- `Conn.FirstError()`：保留首个状态违规错误，后续错误不覆盖。
- `Conn.LiveStreams()` / `Conn.State(id)`：观察资源释放与单流状态（idle/reserved/open/half-closed/closed）。
