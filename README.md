# 区块链JSON-RPC方法门控

攻击者用批量数组、重复method键和大小写变体，想让对外的区块链JSON-RPC端口触达personal_unlockAccount或debug_*。给Go反向代理写供请求入口调用的方法门控包：按JSON-RPC 2.0解析，批量中任一元素不在白名单则整批拒绝，重复键拒绝，方法名精确匹配；eth_getLogs的区块跨度、批量条数有上限。用混入危险方法的批量、重复键和超跨度验证。
