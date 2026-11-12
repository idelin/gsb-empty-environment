// WebTorrent tracker 公告 / 应答校验包。
//
// 供 WebSocket 消息处理层调用，负责：
// - info_hash / peer_id 的 latin1 20 字节解码校验；
// - 单条 WebSocket 连接与 peer_id 的绑定（防冒用、防顶替）；
// - 公告 offers 数量（numwant 与 10 取小）与 sdp 大小（8 KiB）限制；
// - answer 与待决 offer 的绑定校验，且每个 offer_id 只能应答一次。

// 二进制标识（info_hash、peer_id）的固定长度，单位字节。
export const ID_BYTE_LENGTH = 20

// 单条公告允许携带的最大 offer 数（与 numwant 取较小值）。
export const MAX_OFFERS = 10

// 单条 sdp 允许的最大长度，单位字节（8 KiB）。
export const MAX_SDP_BYTES = 8 * 1024

// 校验失败的原因码，消息处理层可据此返回 error 消息或关闭连接。
export type RejectReason =
  | 'invalid_info_hash'
  | 'invalid_peer_id'
  | 'invalid_to_peer_id'
  | 'invalid_numwant'
  | 'too_many_offers'
  | 'invalid_offer_id'
  | 'sdp_too_large'
  | 'peer_id_rebound'
  | 'peer_id_taken'
  | 'not_bound'
  | 'offer_not_pending'
  | 'offer_already_answered'
  | 'answer_target_mismatch'
  | 'answerer_mismatch'

// 校验成功结果。
export interface ValidationOk<T> {
  ok: true
  value: T
}

// 校验失败结果。
export interface ValidationErr {
  ok: false
  reason: RejectReason
  message: string
}

export type ValidationResult<T> = ValidationOk<T> | ValidationErr

// 公告中的单个 offer（tracker 视角）。
export interface OfferItem {
  offer_id: string | unknown
  sdp: string | unknown
}

// 客户端发来的 announce 消息。
export interface AnnounceMessage {
  action: 'announce'
  info_hash: string | unknown
  peer_id: string | unknown
  numwant?: number | unknown
  offers?: OfferItem[] | unknown
}

// 客户端发来的 answer 消息。
export interface AnswerMessage {
  action: 'answer'
  info_hash: string | unknown
  peer_id: string | unknown
  to_peer_id: string | unknown
  offer_id: string | unknown
  sdp?: string | unknown
}

// 校验通过后的规范化公告。
export interface NormalizedAnnounce {
  infoHash: string
  peerId: string
  numwant: number
  offers: Array<{ offerId: string; sdp: string }>
}

// 校验通过后的规范化应答。
export interface NormalizedAnswer {
  infoHash: string
  peerId: string
  toPeerId: string
  offerId: string
  sdp: string
}

// 待决 offer 的登记信息。
interface PendingOffer {
  offerId: string
  infoHash: string
  // offer 发出方（即 answer 的目标 to_peer_id）。
  fromPeerId: string
  // offer 接收方（唯一允许应答的 peer）。
  toPeerId: string
  answered: boolean
}

function fail(reason: RejectReason, message: string): ValidationErr {
  return { ok: false, reason, message }
}

// 将字符串按 latin1 解码为恰好 20 字节。
// 任何码元大于 0xFF 的字符（无法用单字节 latin1 表示）或长度不为 20 都拒绝。
export function decodeId20(value: unknown, field: string): ValidationResult<string> {
  const reason: RejectReason =
    field === 'info_hash' ? 'invalid_info_hash' : field === 'to_peer_id' ? 'invalid_to_peer_id' : 'invalid_peer_id'
  if (typeof value !== 'string') {
    return fail(reason, `${field} must be a latin1 string of exactly ${ID_BYTE_LENGTH} bytes`)
  }
  if (value.length !== ID_BYTE_LENGTH) {
    return fail(reason, `${field} must decode to exactly ${ID_BYTE_LENGTH} bytes, got ${value.length}`)
  }
  for (let index = 0; index < value.length; index += 1) {
    if (value.charCodeAt(index) > 0xff) {
      return fail(reason, `${field} contains a character outside the latin1 range (0x00-0xff)`)
    }
  }
  return { ok: true, value }
}

// 以 utf-8 字节数衡量 sdp 大小。
function sdpByteLength(sdp: string): number {
  return Buffer.byteLength(sdp, 'utf8')
}

// Tracker 校验器：维护连接绑定与待决 offer 状态。
// 泛型 Conn 由消息处理层决定（通常是 WebSocket 对象），仅用作 Map 键。
export class TrackerValidator<Conn = object> {
  // 连接 -> 首次公告绑定的 peer_id。
  private readonly binding = new Map<Conn, string>()
  // peer_id -> 当前持有该 peer_id 的连接。
  private readonly owner = new Map<string, Conn>()
  // offer_id -> 待决 / 已应答的 offer。
  private readonly offers = new Map<string, PendingOffer>()

  // 校验并登记一条 announce。
  //
  // 成功后该连接与 peer_id 绑定：同一连接换 peer_id 拒绝（peer_id_rebound），
  // 另一连接使用已被占用的 peer_id 拒绝（peer_id_taken）而不是顶替。
  validateAnnounce(conn: Conn, message: AnnounceMessage): ValidationResult<NormalizedAnnounce> {
    const infoHashResult = decodeId20(message.info_hash, 'info_hash')
    if (!infoHashResult.ok) return infoHashResult
    const peerIdResult = decodeId20(message.peer_id, 'peer_id')
    if (!peerIdResult.ok) return peerIdResult

    const infoHash = infoHashResult.value
    const peerId = peerIdResult.value

    const boundPeerId = this.binding.get(conn)
    if (boundPeerId !== undefined && boundPeerId !== peerId) {
      return fail('peer_id_rebound', 'connection is already bound to a different peer_id')
    }
    const ownerConn = this.owner.get(peerId)
    if (ownerConn !== undefined && ownerConn !== conn) {
      return fail('peer_id_taken', 'peer_id is already in use by another connection')
    }

    let numwant = MAX_OFFERS
    if (message.numwant !== undefined && message.numwant !== null) {
      if (typeof message.numwant !== 'number' || !Number.isInteger(message.numwant) || message.numwant < 0) {
        return fail('invalid_numwant', 'numwant must be a non-negative integer')
      }
      numwant = message.numwant
    }
    const offerLimit = Math.min(numwant, MAX_OFFERS)

    const rawOffers = message.offers
    let offers: NormalizedAnnounce['offers'] = []
    if (rawOffers !== undefined) {
      if (!Array.isArray(rawOffers)) {
        return fail('too_many_offers', 'offers must be an array')
      }
      if (rawOffers.length > offerLimit) {
        return fail(
          'too_many_offers',
          `offers length ${rawOffers.length} exceeds limit ${offerLimit} (min(numwant, ${MAX_OFFERS}))`,
        )
      }
      for (const item of rawOffers) {
        if (item === null || typeof item !== 'object') {
          return fail('invalid_offer_id', 'each offer must have an offer_id and an sdp')
        }
        if (typeof item.offer_id !== 'string' || item.offer_id.length === 0) {
          return fail('invalid_offer_id', 'offer.offer_id must be a non-empty string')
        }
        if (typeof item.sdp !== 'string') {
          return fail('sdp_too_large', 'offer.sdp must be a string')
        }
        if (sdpByteLength(item.sdp) > MAX_SDP_BYTES) {
          return fail('sdp_too_large', `offer.sdp exceeds ${MAX_SDP_BYTES} bytes`)
        }
        offers.push({ offerId: item.offer_id, sdp: item.sdp })
      }
    }

    this.binding.set(conn, peerId)
    this.owner.set(peerId, conn)

    return { ok: true, value: { infoHash, peerId, numwant, offers } }
  }

  // 在 tracker 把 offer 转发给目标 peer 后登记待决 offer。
  // 同一 offer_id 重复登记拒绝；只有登记过的 offer 才允许被应答。
  registerOffer(params: {
    offerId: string
    infoHash: string
    fromPeerId: string
    toPeerId: string
  }): ValidationResult<void> {
    if (this.offers.has(params.offerId)) {
      return fail('offer_already_answered', `offer_id ${params.offerId} is already registered`)
    }
    this.offers.set(params.offerId, {
      offerId: params.offerId,
      infoHash: params.infoHash,
      fromPeerId: params.fromPeerId,
      toPeerId: params.toPeerId,
      answered: false,
    })
    return { ok: true, value: undefined }
  }

  // 校验一条 answer：
  // - 应答连接必须已绑定 peer_id，且与消息中的 peer_id 一致；
  // - offer_id 必须存在且尚未应答（每个 offer_id 只能应答一次）；
  // - 待决 offer 的接收方必须是该应答者（peer_id）；
  // - 待决 offer 的发出方必须是目标 to_peer_id；
  // - info_hash 必须与登记 offer 时一致。
  //
  // 校验通过即把该 offer 标记为已应答，调用方可安全转发。
  validateAnswer(conn: Conn, message: AnswerMessage): ValidationResult<NormalizedAnswer> {
    const infoHashResult = decodeId20(message.info_hash, 'info_hash')
    if (!infoHashResult.ok) return infoHashResult
    const peerIdResult = decodeId20(message.peer_id, 'peer_id')
    if (!peerIdResult.ok) return peerIdResult
    const toPeerIdResult = decodeId20(message.to_peer_id, 'to_peer_id')
    if (!toPeerIdResult.ok) return toPeerIdResult

    const infoHash = infoHashResult.value
    const peerId = peerIdResult.value
    const toPeerId = toPeerIdResult.value

    const boundPeerId = this.binding.get(conn)
    if (boundPeerId === undefined) {
      return fail('not_bound', 'connection has not announced and cannot send answers')
    }
    if (boundPeerId !== peerId) {
      return fail('peer_id_rebound', 'answer peer_id does not match the peer_id bound to this connection')
    }

    if (typeof message.offer_id !== 'string' || message.offer_id.length === 0) {
      return fail('invalid_offer_id', 'offer_id must be a non-empty string')
    }
    const sdp = typeof message.sdp === 'string' ? message.sdp : ''
    if (message.sdp !== undefined && typeof message.sdp !== 'string') {
      return fail('sdp_too_large', 'answer.sdp must be a string')
    }
    if (sdpByteLength(sdp) > MAX_SDP_BYTES) {
      return fail('sdp_too_large', `answer.sdp exceeds ${MAX_SDP_BYTES} bytes`)
    }

    const pending = this.offers.get(message.offer_id)
    if (pending === undefined) {
      return fail('offer_not_pending', 'no pending offer matches this offer_id')
    }
    if (pending.answered) {
      return fail('offer_already_answered', 'this offer_id has already been answered')
    }
    if (pending.infoHash !== infoHash) {
      return fail('offer_not_pending', 'offer info_hash does not match the answer')
    }
    if (pending.fromPeerId !== toPeerId) {
      return fail('answer_target_mismatch', 'to_peer_id is not the peer that issued this offer')
    }
    if (pending.toPeerId !== peerId) {
      return fail('answerer_mismatch', 'this connection is not the designated recipient of the offer')
    }

    pending.answered = true
    return { ok: true, value: { infoHash, peerId, toPeerId, offerId: pending.offerId, sdp } }
  }

  // 连接关闭时释放 peer_id 绑定；待决 offer 保留登记以拒绝迟到应答。
  releaseConnection(conn: Conn): void {
    const peerId = this.binding.get(conn)
    if (peerId === undefined) return
    this.binding.delete(conn)
    if (this.owner.get(peerId) === conn) {
      this.owner.delete(peerId)
    }
  }

  // 查询某连接当前绑定的 peer_id，主要供消息处理层 / 测试使用。
  getBoundPeerId(conn: Conn): string | undefined {
    return this.binding.get(conn)
  }
}
