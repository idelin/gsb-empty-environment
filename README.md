# fsconfig选项覆盖与创建后修改

fsopen、fsconfig、fsmount 把挂载拆成多步，攻击者能在配置阶段反复设置参数，用同名选项覆盖先前的安全选项，或经 FSCONFIG_SET_FD 传入别人的描述符。为 Go 容器运行时写供挂载上下文状态机调用的校验包：输入文件系统类型与 fsconfig 命令序列（SET_FLAG、SET_STRING、SET_BINARY、SET_PATH、SET_FD、CMD_CREATE、CMD_RECONFIGURE）；选项白名单按文件系统类型登记；nosuid、nodev、noexec 一旦设置，不得被后续同键 SET_FLAG 或字符串形式（suid、dev、exec）覆盖；CMD_CREATE 之后再 SET_* 拒绝；SET_FD 的描述符须是调用方登记的；仅 reconfigure 阶段可用的键不得在创建阶段使用；最终 fsmount 的 MOUNT_ATTR_ 位不得弱于配置阶段已声明的保护位。用覆盖 nosuid、创建后设置、未登记描述符和合规序列验证。
