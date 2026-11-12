# WebTorrent公告与应答绑定

WebTorrent tracker 通过 WebSocket 转发 offer 与 answer，攻击者可冒用别人的 peer_id 发公告，或把 answer 发给从未发过 offer 的对象。给TypeScript tracker 写供消息处理调用的校验包：info_hash 与 peer_id 按 latin1 解为恰好 20 字节，含超出 0xFF 的字符拒绝；一个 WebSocket 连接首次公告后绑定其 peer_id，之后不同 peer_id 的公告拒绝，同一 peer_id 出现在另一条连接时拒绝而不是顶替；每条公告 offers 数不超过 numwant 与 10 中较小者，sdp 不超过 8KiB；answer 只在其 offer_id 确由目标 to_peer_id 的待决 offer 发给该应答者时转发，且一个 offer_id 只能被应答一次。用冒用 peer_id、重复 offer_id 应答、无待决 offer 的 answer 与超限 offers 验证。
