# Sparkplug主题权限与遗嘱序号绑定

攻击者订阅或发布 Sparkplug 命令主题，或伪造某个边缘节点的 NDEATH 让它显示离线。为TypeScript MQTT 代理写可导入的 ACL 判定包：NCMD 与 DCMD 只允许主应用凭据发布；边缘节点只能发布 spBv1.0/自己所属组/NBIRTH、NDATA、NDEATH，节点名等于自己的客户端标识；NDEATH 的 bdSeq 必须等于该节点最近 NBIRTH 的 bdSeq；同一节点的 Rebirth 命令每分钟不超过 3 次。用越权发布、伪造 NDEATH 和命令洪泛验证。
