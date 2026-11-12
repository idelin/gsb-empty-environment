# WebSub订阅意图验证

WebSub 中心收到订阅请求后须向回调地址确认意图，攻击者替受害者的回调地址批量订阅，或在确认时猜测挑战串。为Go的 WebSub 中心写可导入的订阅管理包：收到 hub.mode=subscribe 先返回 202，再以 GET 向 hub.callback 发送 hub.mode、hub.topic、hub.challenge、hub.lease_seconds；回调须返回 2xx 且正文与 hub.challenge 逐字节相等才生效，challenge 为至少 16 字节随机数的十六进制；lease_seconds 取请求值与上限 864000 中较小者；hub.secret 须小于 200 字节，内容分发时在 X-Hub-Signature 头里带 `sha256=` 加 HMAC-SHA256 十六进制；回调为非 HTTPS 或解析到私网地址拒绝；同一回调对同一 topic 重复订阅只更新租期；同一回调每小时未验证的订阅请求超过 20 个即拒绝。测错误挑战串、私网回调、租期上限、未验证洪泛。
