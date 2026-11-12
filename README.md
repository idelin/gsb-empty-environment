# TUF回滚与冻结防护

软件更新客户端会遇到镜像回放旧元数据（回滚）或停发新元数据（冻结）。为Go更新库实现供下载流程调用的 TUF 元数据校验包：按 root、timestamp、snapshot、targets 的顺序，各角色签名门限由受信 root 指定；新 root 须同时被旧 root 与自身的密钥集达到门限，版本号恰好加 1；timestamp、snapshot、targets 的版本不得小于已存版本，snapshot 记录的 targets 版本及哈希须与实际一致，过期时间早于注入时钟的元数据拒绝；root 轮换后须丢弃旧的 timestamp 与 snapshot。用版本回退、过期冻结、只被新密钥签的 root 和合法轮换验证。
