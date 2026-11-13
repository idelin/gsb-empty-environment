# memfd封印写后固定

沙箱把配置或可执行内容放进 memfd 后交给不可信进程，若不封印，对方仍能在检查通过后改写内容；存在可写共享映射时 F_SEAL_WRITE 加不上，F_SEAL_FUTURE_WRITE 又允许已有映射继续写。为 Go 沙箱宿主写供传递描述符前调用的封印状态机包：输入对 memfd 的操作序列（ftruncate、write、共享可写 mmap、私有 mmap、munmap、fcntl 加封印、dup 传递），模拟 F_SEAL_SEAL、SHRINK、GROW、WRITE、FUTURE_WRITE 的效果；存在可写共享映射时加 F_SEAL_WRITE 返回 EBUSY；F_SEAL_FUTURE_WRITE 之后已有映射仍可写，因此不满足写后封印；创建时未带 MFD_ALLOW_SEALING 的 memfd 不可加封印；交付条件为 WRITE、SHRINK、GROW、SEAL 全封且无可写映射。用残留映射、仅 FUTURE_WRITE、先封 SEAL 后加封印和合规序列验证。
