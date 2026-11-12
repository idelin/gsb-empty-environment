# Expect 100-continue鉴权前正文

客户端带 Expect: 100-continue 时，服务端在鉴权通过前就回 100，会让攻击者先用大正文占住后端，或借多个 Expect 头与代理层的理解差异发起走私。为Go反向代理写可导入的 Expect 处理包，供请求处理调用：Expect 的值只认 100-continue（不分大小写），其他值返回 417；HTTP/1.0 请求带 Expect 忽略；后端鉴权结果返回前不得发送 100，鉴权失败时返回最终状态并关闭连接，不读取请求正文；Content-Length 超过调用方上限时直接返回 413，不发 100；同一请求出现多个 Expect 头，或 Transfer-Encoding 与 Content-Length 并存，一律拒绝；客户端未等 100 就发送正文时，这些字节计入正文预算，超限即关闭；向后端转发时 Expect 已被消耗，不得再转发 Expect 头。用鉴权失败不读正文、超大 Content-Length、多个 Expect、HTTP/1.0 和未等 100 就发正文验证。
