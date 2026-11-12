# WebTorrent公告与应答绑定

WebTorrent tracker 通过 WebSocket 转发 offer 与 answer，攻击者可冒用别人的 peer_id 发公告，或把 answer 发给从未发过 offer 的对象。给TypeScript tracker 写供消息处理调用的校验包：info_hash 与 peer_id 按 latin1 解为恰好 20 字节，含超出 0xFF 的字符拒绝；一个 WebSocket 连接首次公告后绑定其 peer_id，之后不同 peer_id 的公告拒绝，同一 peer_id 出现在另一条连接时拒绝而不是顶替；每条公告 offers 数不超过 numwant 与 10 中较小者，sdp 不超过 8KiB；answer 只在其 offer_id 确由目标 to_peer_id 的待决 offer 发给该应答者时转发，且一个 offer_id 只能被应答一次。用冒用 peer_id、重复 offer_id 应答、无待决 offer 的 answer 与超限 offers 验证。

## 交付内容

- `src/validator.ts`：无依赖校验包，导出 `TrackerValidator`、`decodeId20` 与常量。
- `test/validator.test.ts`：基于 `node --test` 的 22 个用例，`npm test` 运行。
- `index.ts`：包入口，re-export 校验器与消息类型。

## 消息处理层集成方式

```ts
import { TrackerValidator } from './index.ts'

const validator = new TrackerValidator<WebSocket>()

ws.on('close', () => validator.releaseConnection(ws))

ws.on('message', (raw) => {
  const msg = JSON.parse(raw.toString())

  if (msg.action === 'announce') {
    const result = validator.validateAnnounce(ws, msg)
    if (!result.ok) return ws.send(JSON.stringify({ action: 'error', reason: result.reason }))

    for (const offer of result.value.offers) {
      // 选定目标 peer 后登记，再转发 offer
      validator.registerOffer({
        offerId: offer.offerId,
        infoHash: result.value.infoHash,
        fromPeerId: result.value.peerId,
        toPeerId: targetPeerId,
      })
    }
  }

  if (msg.action === 'answer') {
    const result = validator.validateAnswer(ws, msg)
    if (!result.ok) return ws.send(JSON.stringify({ action: 'error', reason: result.reason }))
    // 仅当 result.ok 时转发给 result.value.toPeerId
  }
})
```

## 拒绝原因码

- `invalid_info_hash` / `invalid_peer_id` / `invalid_to_peer_id`：非字符串、长度不为 20 或含超出 0xFF 的字符。
- `peer_id_rebound`：连接已绑定其他 peer_id 后更换 peer_id（含 answer 冒名）。
- `peer_id_taken`：peer_id 已被另一条连接占用，拒绝顶替。
- `too_many_offers`：offers 数量超过 `min(numwant, 10)`；`invalid_numwant` 表示 numwant 非法。
- `sdp_too_large`：sdp 超过 8192 字节（按 utf-8 计）。
- `offer_not_pending`：offer_id 无待决登记或 info_hash 不匹配。
- `offer_already_answered`：offer_id 已应答，或重复登记同一 offer_id。
- `answer_target_mismatch`：`to_peer_id` 不是该 offer 的发出方。
- `answerer_mismatch`：应答连接不是该 offer 的指定接收方。
- `not_bound`：未公告过的连接直接发 answer。
