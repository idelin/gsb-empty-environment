# StoreKit JWS证书链与OID

服务端验证 App Store 的 signedTransactionInfo，攻击者自建证书链签出一份看似合法的交易。为Go服务实现可导入的 JWS 校验包：头里 alg 只许 ES256，x5c 恰为三张证书（叶、中间、根），根须与调用方内置的 Apple 根证书 DER 完全相同；证书链按载荷 signedDate 时刻验证有效期；叶证书须带 OID 1.2.840.113635.100.6.11.1，中间证书须带 1.2.840.113635.100.6.2.1；载荷里 bundleId 须等于本应用，environment 与服务端当前环境一致，存在 revocationDate 则判为已撤销。用自签链、缺 OID、有效期边界与沙箱交易验证。
