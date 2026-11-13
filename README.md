# io_uring同步取消的租户范围

io_uring 的 IORING_REGISTER_SYNC_CANCEL 能同步取消按 user_data、文件描述符或操作码匹配的在途请求，若不限范围，持有环的沙箱代码可用通配取消同一环里其他租户的请求。为 Go 宿主写供环注册路径调用的取消授权包：环由多个租户共享，每个在途请求记录租户、user_data、fd、opcode；取消请求的匹配条件含 ANY、ALL、FD、FD_FIXED、OP、USERDATA，租户只能取消自己的请求，ANY 与 ALL 只作用于调用者自己的请求，FD_FIXED 的槽号按调用者自己的固定文件表解析；超时参数大于 10 秒拒绝；返回取消数，且不得泄露其他租户请求是否存在。用 ANY 通配、他人 user_data 命中、固定槽号错位、超时过长和取消自己的请求验证。
