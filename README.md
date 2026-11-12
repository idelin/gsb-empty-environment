# GHCB响应校验

SEV-ES 客户机的 #VC 处理程序通过共享的 GHCB 页让宿主模拟 CPUID、MSR 与端口 I/O，宿主可回填任意值（Iago 攻击）。为C++17客户机固件实现供 #VC 处理程序调用的响应校验组件：请求前清空 valid_bitmap，响应只能包含该退出码允许的字段（CPUID 返回 rax、rbx、rcx、rdx，端口读返回 rax，端口写无返回）且对应位已置；sw_exit_info1 非 0 表示失败须按错误处理；scratch 区必须落在 GHCB 共享区内；端口读响应不得超出请求宽度，超出位清零；CPUID 结果的特性位须是调用方可信表的子集。用多余字段、缺位、失败码、scratch 越界和合法 CPUID 验证。
