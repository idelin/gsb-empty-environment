# EAP-TLS 1.3成功帧承诺

EAP-TLS 1.3服务端若在TLS完成前发出EAP-Success，攻击者可伪造该消息使客户端以为认证已结束。为Go 802.1X接入组件实现客户端状态包，供EAP方法回调调用：收到最终握手消息并完成空的commitment消息后才接受EAP-Success；恢复握手须取回原始用户身份并重新查询授权。用提前Success、缺失commitment和身份被撤销后的恢复验证。
