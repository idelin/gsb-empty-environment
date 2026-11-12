# Redis库绑定与事务穿透

受限用户只许用 0 号库，却想借 SELECT、MOVE、COPY 的 DB 参数、SWAPDB 或事务里的 SELECT 触达其他库。为Go Redis 代理写可导入的库绑定门禁：连接状态跟踪当前库，pipeline 与 MULTI/EXEC 内的 SELECT 同样生效；允许库之外的 SELECT、MOVE、SWAPDB 与带 DB 参数的 COPY 以错误应答拒绝；EVAL 与 FUNCTION 对受限用户整体拒绝。测管道、事务、命令名大小写、拒绝后的连接状态。
