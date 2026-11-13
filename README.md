# io_uring同步取消的租户范围

io_uring 的 IORING_REGISTER_SYNC_CANCEL 能同步取消按 user_data、文件描述符或操作码匹配的在途请求，若不限范围，持有环的沙箱代码可用通配取消同一环里其他租户的请求。为 Go 宿主写供环注册路径调用的取消授权包：环由多个租户共享，每个在途请求记录租户、user_data、fd、opcode；取消请求的匹配条件含 ANY、ALL、FD、FD_FIXED、OP、USERDATA，租户只能取消自己的请求，ANY 与 ALL 只作用于调用者自己的请求，FD_FIXED 的槽号按调用者自己的固定文件表解析；超时参数大于 10 秒拒绝；返回取消数，且不得泄露其他租户请求是否存在。用 ANY 通配、他人 user_data 命中、固定槽号错位、超时过长和取消自己的请求验证。

## 包设计（`package netsec`）

宿主在处理 `IORING_REGISTER_SYNC_CANCEL` 时，先调用 `Authorizer.SyncCancel`
授权，授权通过后才允许操作共享环。

- 数据结构：每个租户独立的 `tenantState`（在途请求表 + 固定文件表），取消路径
  只索引调用者自己的状态，结构上无法遍历到其他租户；`sync.RWMutex` 保证并发安全。
- 匹配标志：`CancelAny/CancelAll/CancelFD/CancelFDFixed/CancelOp/CancelUserData`
  对应 `IORING_ASYNC_CANCEL_*`。`ANY` 取消调用者全部在途请求且不得与其他标志组合；
  具体选择器在无 `ALL` 时只取消首个匹配，带 `ALL` 时取消全部匹配。
- 固定文件：`FD_FIXED` 的槽号通过调用者自己的固定文件表解析成宿主 fd 后再匹配，
  槽号未注册返回 `ErrInvalidFixedSlot`，绝不查询其他租户的表。
- 超时：负值返回 `ErrInvalidTimeout`，超过 `MaxCancelTimeout`（10s）返回
  `ErrTimeoutTooLarge`，且在检查任何请求之前拒绝。
- 防泄漏：他人 `user_data`/fd/opcode 命中与“无人拥有该值”返回完全相同的
  `ErrNoMatch`，错误文本一致，无法据此判断其他租户是否存在对应请求。

## 验证

`go test -race -count=1 ./...` 覆盖：ANY 通配仅限本租户、他人 user_data 不可达且
错误不可区分、固定槽号错位（同槽不同 fd / 共享 fd 仍租户隔离 / 未注册槽拒绝）、
超时边界（10s 含边界接受，超出与负值拒绝）、取消自己请求的计数与组合条件、
非法标志组合、未知租户探测无泄漏以及并发竞态。
