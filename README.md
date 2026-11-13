# HTTP2禁用连接头

HTTP/2里带上transfer-encoding或connection后，降级到HTTP/1就会变成走私。伪头之外禁止connection、transfer-encoding、keep-alive、upgrade；出现即整流失败。普通自定义头保留。协议网关调用可导入的Go库做这次裁决。在合法请求上分别加这四个头。
