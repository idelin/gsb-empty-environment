import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import {
  MAX_OFFERS,
  MAX_SDP_BYTES,
  TrackerValidator,
  decodeId20,
} from '../src/validator.ts'
import type { AnnounceMessage, AnswerMessage } from '../src/validator.ts'

const id = (char: string): string => char.repeat(20)
const INFO_HASH = id('I')
const PEER_A = id('A')
const PEER_B = id('B')
const PEER_C = id('C')

function announce(
  peerId: string,
  overrides: Partial<AnnounceMessage> = {},
): AnnounceMessage {
  return {
    action: 'announce',
    info_hash: INFO_HASH,
    peer_id: peerId,
    ...overrides,
  }
}

function makeOffers(count: number, sdp = 'v=0'): NonNullable<AnnounceMessage['offers']> {
  return Array.from({ length: count }, (_unused, index) => ({
    offer_id: `offer-${index}`,
    sdp,
  }))
}

function answer(overrides: Partial<AnswerMessage> = {}): AnswerMessage {
  return {
    action: 'answer',
    info_hash: INFO_HASH,
    peer_id: PEER_B,
    to_peer_id: PEER_A,
    offer_id: 'offer-1',
    sdp: 'v=0',
    ...overrides,
  }
}

describe('decodeId20: latin1 20 字节校验', () => {
  it('接受恰好 20 字节的 latin1 字符串', () => {
    const result = decodeId20(id('\xff'), 'peer_id')
    assert.equal(result.ok, true)
  })

  it('拒绝长度不足 20 字节', () => {
    const result = decodeId20('short', 'info_hash')
    assert.equal(result.ok, false)
    if (!result.ok) assert.equal(result.reason, 'invalid_info_hash')
  })

  it('拒绝长度超过 20 字节', () => {
    const result = decodeId20(id('A') + 'X', 'peer_id')
    assert.equal(result.ok, false)
    if (!result.ok) assert.equal(result.reason, 'invalid_peer_id')
  })

  it('拒绝码元超出 0xFF 的字符', () => {
    const result = decodeId20('中'.padEnd(20, 'A'), 'peer_id')
    assert.equal(result.ok, false)
    if (!result.ok) assert.equal(result.reason, 'invalid_peer_id')
  })

  it('拒绝非字符串', () => {
    const result = decodeId20(Buffer.alloc(20), 'info_hash')
    assert.equal(result.ok, false)
    if (!result.ok) assert.equal(result.reason, 'invalid_info_hash')
  })
})

describe('announce: peer_id 连接绑定（防冒用 / 防顶替）', () => {
  it('同一连接首次公告后换 peer_id 被拒绝', () => {
    const validator = new TrackerValidator()
    const conn = {}
    assert.equal(validator.validateAnnounce(conn, announce(PEER_A)).ok, true)

    const rebound = validator.validateAnnounce(conn, announce(PEER_B))
    assert.equal(rebound.ok, false)
    if (!rebound.ok) assert.equal(rebound.reason, 'peer_id_rebound')

    assert.equal(validator.getBoundPeerId(conn), PEER_A)
  })

  it('另一连接冒用已占用的 peer_id 被拒绝且不顶替原连接', () => {
    const validator = new TrackerValidator()
    const connA = { name: 'a' }
    const connAttacker = { name: 'attacker' }
    assert.equal(validator.validateAnnounce(connA, announce(PEER_A)).ok, true)

    const spoof = validator.validateAnnounce(connAttacker, announce(PEER_A))
    assert.equal(spoof.ok, false)
    if (!spoof.ok) assert.equal(spoof.reason, 'peer_id_taken')

    // 原连接绑定未被顶替，攻击者连接仍未绑定
    assert.equal(validator.getBoundPeerId(connA), PEER_A)
    assert.equal(validator.getBoundPeerId(connAttacker), undefined)
    assert.equal(validator.validateAnnounce(connA, announce(PEER_A)).ok, true)
  })

  it('连接释放后 peer_id 可被新连接重新绑定', () => {
    const validator = new TrackerValidator()
    const connA = { name: 'a' }
    const connB = { name: 'b' }
    validator.validateAnnounce(connA, announce(PEER_A))
    validator.releaseConnection(connA)

    assert.equal(validator.validateAnnounce(connB, announce(PEER_A)).ok, true)
    assert.equal(validator.getBoundPeerId(connB), PEER_A)
  })

  it('info_hash 含超出 0xFF 字符时公告被拒绝', () => {
    const validator = new TrackerValidator()
    const result = validator.validateAnnounce({}, announce(PEER_A, { info_hash: '中'.padEnd(20, 'I') }))
    assert.equal(result.ok, false)
    if (!result.ok) assert.equal(result.reason, 'invalid_info_hash')
  })
})

describe('announce: offers 数量与 sdp 大小限制', () => {
  it('offers 数不超过 numwant（numwant 更小时以 numwant 为准）', () => {
    const validator = new TrackerValidator()
    const conn = {}
    const rejected = validator.validateAnnounce(conn, announce(PEER_A, { numwant: 3, offers: makeOffers(4) }))
    assert.equal(rejected.ok, false)
    if (!rejected.ok) assert.equal(rejected.reason, 'too_many_offers')

    const accepted = validator.validateAnnounce(conn, announce(PEER_A, { numwant: 3, offers: makeOffers(3) }))
    assert.equal(accepted.ok, true)
    if (accepted.ok) assert.equal(accepted.value.offers.length, 3)
  })

  it('缺省 numwant 时上限为 10', () => {
    const validator = new TrackerValidator()
    const conn = {}
    assert.equal(
      validator.validateAnnounce(conn, announce(PEER_A, { offers: makeOffers(MAX_OFFERS + 1) })).ok,
      false,
    )
    const accepted = validator.validateAnnounce(conn, announce(PEER_A, { offers: makeOffers(MAX_OFFERS) }))
    assert.equal(accepted.ok, true)
  })

  it('numwant 大于 10 时上限仍为 10', () => {
    const validator = new TrackerValidator()
    const result = validator.validateAnnounce(
      {},
      announce(PEER_A, { numwant: 50, offers: makeOffers(11) }),
    )
    assert.equal(result.ok, false)
    if (!result.ok) assert.equal(result.reason, 'too_many_offers')
  })

  it('numwant 为 0 时不允许携带任何 offer', () => {
    const validator = new TrackerValidator()
    const result = validator.validateAnnounce({}, announce(PEER_A, { numwant: 0, offers: makeOffers(1) }))
    assert.equal(result.ok, false)
    if (!result.ok) assert.equal(result.reason, 'too_many_offers')
  })

  it('sdp 恰好 8 KiB 放行，超过 8 KiB 拒绝', () => {
    const validator = new TrackerValidator()
    const conn = {}
    const atLimit = validator.validateAnnounce(
      conn,
      announce(PEER_A, { offers: makeOffers(1, 'x'.repeat(MAX_SDP_BYTES)) }),
    )
    assert.equal(atLimit.ok, true)

    const overLimit = validator.validateAnnounce(
      conn,
      announce(PEER_A, { offers: makeOffers(1, 'x'.repeat(MAX_SDP_BYTES + 1)) }),
    )
    assert.equal(overLimit.ok, false)
    if (!overLimit.ok) assert.equal(overLimit.reason, 'sdp_too_large')
  })
})

describe('answer: 与待决 offer 绑定', () => {
  function setup(): { validator: TrackerValidator; connA: object; connB: object; connC: object } {
    const validator = new TrackerValidator()
    const connA = { name: 'a' }
    const connB = { name: 'b' }
    const connC = { name: 'c' }
    validator.validateAnnounce(connA, announce(PEER_A))
    validator.validateAnnounce(connB, announce(PEER_B))
    validator.validateAnnounce(connC, announce(PEER_C))
    // tracker 把 A 的 offer-1 转发给 B 后登记
    const registered = validator.registerOffer({
      offerId: 'offer-1',
      infoHash: INFO_HASH,
      fromPeerId: PEER_A,
      toPeerId: PEER_B,
    })
    assert.equal(registered.ok, true)
    return { validator, connA, connB, connC }
  }

  it('offer 的指定接收方可以应答，且规范化结果可直接转发', () => {
    const { validator, connB } = setup()
    const result = validator.validateAnswer(connB, answer())
    assert.equal(result.ok, true)
    if (result.ok) {
      assert.equal(result.value.offerId, 'offer-1')
      assert.equal(result.value.peerId, PEER_B)
      assert.equal(result.value.toPeerId, PEER_A)
    }
  })

  it('同一 offer_id 重复应答被拒绝（只能应答一次）', () => {
    const { validator, connB } = setup()
    assert.equal(validator.validateAnswer(connB, answer()).ok, true)
    const duplicate = validator.validateAnswer(connB, answer())
    assert.equal(duplicate.ok, false)
    if (!duplicate.ok) assert.equal(duplicate.reason, 'offer_already_answered')
  })

  it('无待决 offer 的 answer 被拒绝', () => {
    const { validator, connB } = setup()
    const result = validator.validateAnswer(connB, answer({ offer_id: 'never-existed' }))
    assert.equal(result.ok, false)
    if (!result.ok) assert.equal(result.reason, 'offer_not_pending')
  })

  it('to_peer_id 不是 offer 发出方时拒绝转发', () => {
    const { validator, connB } = setup()
    const result = validator.validateAnswer(connB, answer({ to_peer_id: PEER_C }))
    assert.equal(result.ok, false)
    if (!result.ok) {
      assert.equal(result.reason, 'answer_target_mismatch')
    }
  })

  it('非 offer 指定接收方的连接应答被拒绝', () => {
    const { validator, connC } = setup()
    const result = validator.validateAnswer(connC, answer({ peer_id: PEER_C }))
    assert.equal(result.ok, false)
    if (!result.ok) assert.equal(result.reason, 'answerer_mismatch')
  })

  it('冒用他人 peer_id 发 answer 被连接绑定拦截', () => {
    const { validator, connB } = setup()
    // connB 绑定的是 PEER_B，却声称自己是 PEER_C
    const result = validator.validateAnswer(connB, answer({ peer_id: PEER_C }))
    assert.equal(result.ok, false)
    if (!result.ok) assert.equal(result.reason, 'peer_id_rebound')
  })

  it('从未公告的连接发 answer 被拒绝', () => {
    const { validator } = setup()
    const result = validator.validateAnswer({}, answer())
    assert.equal(result.ok, false)
    if (!result.ok) assert.equal(result.reason, 'not_bound')
  })

  it('重复登记同一 offer_id 被拒绝', () => {
    const { validator } = setup()
    const result = validator.registerOffer({
      offerId: 'offer-1',
      infoHash: INFO_HASH,
      fromPeerId: PEER_A,
      toPeerId: PEER_B,
    })
    assert.equal(result.ok, false)
    if (!result.ok) assert.equal(result.reason, 'offer_already_answered')
  })
})
