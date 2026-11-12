# Cap'n Proto RPC导出表隔离

攻击者对Cap'n Proto RPC服务端猜导出ID发Call，或用多余的Release把引用计数减成负数。给Go服务端写供消息分发器调用的导出表：ID按连接隔离，Release超过计数拒绝，释放的ID在对端确认前不得重分配；衰减后的能力只暴露调用方的方法白名单，目标不在本连接导出表则断开连接。用猜ID、超额Release和ID复用验证。
