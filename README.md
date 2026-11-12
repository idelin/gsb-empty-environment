# PAC密钥族轮换

启用指针认证的进程由 fork 服务器派生子进程，子进程共用密钥，攻击者可借一个个崩溃的子进程逐个猜 PAC，也可利用 exec 后密钥未更新的窗口复用旧签名。为C++17内核模型写可嵌入任务管理器的密钥域管理库：fork 继承父密钥，exec 必换新密钥，同一密钥族内累计认证失败达到阈值后，下一个 fork 的子进程分到新密钥，内核入口用独立内核密钥且用户态不可读取。用阈值边界、exec、线程共享、信号帧恢复和读取密钥的调试请求验证。

## 设计

`netsec::KeyDomainManager` 可嵌入任务管理器，按任务（`TaskId`）管理 PAC 密钥域：

- `fork_task`：子进程继承父进程密钥（共享密钥族）；若族已累计认证失败达到阈值（`Config::failure_threshold`），子进程改分新密钥、失败计数清零，父进程保留旧钥直至 exec。
- `exec_task`：无条件轮换新密钥，旧镜像签名立即失效。
- `clone_thread`：线程共享任务的密钥与失败计数。
- `enter_kernel` / `exit_kernel`：内核入口切换到独立内核密钥；内核态失配不计入用户密钥族，内核密钥不提供用户态预言机。
- `save_signal_frame` / `restore_signal_frame`：信号帧以内核密钥 MAC 保护，sigreturn 恢复信号前密钥上下文，篡改或跨任务重放的帧被拒绝。
- `debug_read_key`：ptrace 式调试读取。用户密钥仅授权调试器可读；内核密钥一律不可读。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试见 `tests/key_domain_test.cpp`，覆盖阈值边界、exec 轮换、线程共享、信号帧恢复与调试读钥。
