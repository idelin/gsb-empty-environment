# 跨来源重定向请求方法

安全客户端处理重定向时，方法改写与正文重放必须同时决定。给Go传输宿主实现重定向规划包，按状态码和原方法确定保留或改为GET，删除与被丢弃正文相关的字段，敏感首部按目标来源重新授权；循环及总跳数有界。用本地服务器核对实际方法、正文与头，取消不重复发送已完成一跳。

## 实现

`redirect` 包提供：

- `Planner.Next(req, resp, via)`：按状态码与原方法规划下一跳。307/308 保留方法并经 `GetBody` 重放正文；301/302 将非 GET/HEAD 改写为 GET；303 将非 HEAD 改写为 GET。正文被丢弃时同步删除 `Content-Length`、`Content-Type`、`Transfer-Encoding`。`Authorization`、`Cookie` 等敏感首部仅在目标为初始来源或其子域时转发。`MaxHops` 限制总跳数（默认 10），方法+URL 重复即判循环。
- `Client.Do(req)`：经 `RoundTripper` 逐跳执行，每跳前检查上下文，取消后绝不重发已完成的跳。

测试通过 `net.Pipe` 承载的内存 HTTP/1.1 服务器核对线上的实际方法、正文与首部，无需绑定任何套接字。
