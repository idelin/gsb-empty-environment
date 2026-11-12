# Kafka幂等写入纪元校验

僵尸生产者或窃取 producerId 的攻击者想重写、跳号或插队已确认的消息。为Go分区日志写可导入的幂等写入校验组件：按（producerId，分区）保存 epoch、最近5个批次的序号区间与偏移；epoch 低于当前则 fencing 拒绝，更高时序号从0重来；序号等于最后序号加1才追加，落在缓存批次内返回原偏移，其余返回乱序；int32 最大值后回绕为0；transactionalId 只许其绑定的 producerId。测僵尸写、重复批次、回绕、缺口。
