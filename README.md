# userfaultfd创建与注册门控

userfaultfd 能让攻击者在内核访问用户内存的瞬间卡住线程，把条件竞争窗口拉长。系统有 vm.unprivileged_userfaultfd 开关，UFFD_USER_MODE_ONLY 标志则只处理用户态触发的缺页。为 Go 沙箱网关写供 userfaultfd 与 ioctl 拦截调用的授权包：输入 sysctl 取值、调用者能力（CAP_SYS_PTRACE）、创建标志（UFFD_USER_MODE_ONLY、O_CLOEXEC、O_NONBLOCK）及随后的 UFFDIO_API 与 REGISTER 请求；沙箱策略为无论内核开关如何，无 CAP_SYS_PTRACE 者必须带 UFFD_USER_MODE_ONLY；REGISTER 的范围须落在调用者自己的匿名私有映射内，MISSING 之外的模式（WP、MINOR）需要额外许可；描述符经 SCM_RIGHTS 传给他人后，已注册范围的权限判定不变。用缺标志创建、范围越到文件映射、WP 模式和传递描述符验证。
