# SSH扩展请求答复关联

同一SSH连接存在多个全局请求时，成功答复没有独立编号，错误关联可能放开未授权转发。实现供Go会话宿主调用的请求队列，按发送完成次序匹配要求答复的请求，无答复请求不占位；部分写入失败关闭该连接而非重排。用取消、迟到答复及半关闭测试不会把一个批准套给另一请求。

## 用法

会话宿主用 `NewQueue(tr)` 包装连接写端（`io.WriteCloser`），发送全局请求时调用 `Queue.Send(ctx, name, wantReply, payload)`；读循环收到 `SSH_MSG_REQUEST_SUCCESS/FAILURE` 时调用 `Queue.HandleReply(ok, payload)`，连接读侧关闭（含半关闭）时调用 `Queue.Close()`。

- 答复按请求**写入完成**的先后依次匹配；`wantReply=false` 的请求不占队列位置。
- 写入部分失败（`0 < n < len(msg)`）判定为协议失步，立即关闭连接并失败所有等待者，不重排重试。
- 等待中被 `ctx` 取消的请求保留墓碑槽位，其迟到答复仍按序消费并丢弃，不会套给下一个请求。
- 无等待者时收到答复（`ErrUnsolicitedReply`）同样关闭连接。
