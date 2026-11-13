# Macaroons第三方caveat绑定

Macaroons 的第三方 caveat 由外部服务出具 discharge macaroon，攻击者拿别的请求的 discharge 复用，或在追加 caveat 失败后尝试删掉已有 caveat。为Go资源服务写可导入的 macaroon 验证包，供请求授权调用，按本题约定计算：sig0 = HMAC-SHA256(rootKey, identifier)；第一方 caveat 的签名为 HMAC(前一签名, caveat)；第三方 caveat 的签名为 HMAC(前一签名, vid ‖ cid)；每个第三方 caveat 须提供 discharge，调用方的 recoverRootKey(vid) 给出其根密钥；discharge 的绑定签名为 HMAC-SHA256(32 个零字节, 主签名 ‖ discharge 签名)，与请求提供的绑定值不等拒绝；一个 discharge 只对其 cid 有效且在一次验证内只能用一次；第一方 caveat 的谓词由调用方回调判定，未知谓词不满足；time < T 的 caveat 多个并存时取最早；caveat 总数超过 32 拒绝。用复用 discharge、删除 caveat、绑定值错误、未知谓词和多个 time caveat 验证。
