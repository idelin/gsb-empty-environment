# io_uring固定文件权限

io_uring 代理替不可信客户端提交 SQE，注册的 fixed file 槽位会在原 fd 关闭或权限收回后继续可用，攻击者还可用 linked SQE 在槽位之间混用权限。写供 Go 代理调用的 SQE 校验包，输入 64 字节 SQE 和带凭据快照的槽位表，校验 opcode 白名单、IOSQE_FIXED_FILE 下标、槽位读写权限、personality 与链上后续 SQE；权限撤销后同一槽位不得再放行。用越界槽位、只读槽写入、链内换槽和撤销后重用验证。
