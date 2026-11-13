# 重试cookie回显

第二次ClientHello没有回显cookie，服务器仍继续。若重试带了cookie，第二次必须原样回显；缺失或改动则失败。原样回显的cookie继续握手。由TLS栈调用可导入的Go库。翻转cookie的一个字节。
