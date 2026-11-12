# WebAuthn关联来源判定

多个品牌域共用一个RP ID，攻击者会用相似域名或被劫持的子域发起断言。为Go认证服务实现WebAuthn来源判定包，供断言校验调用：origin须在/.well-known/webauthn清单内（至多五个可注册域标签），rpIdHash对应该RP ID；crossOrigin为真或带topOrigin时顶层页也须在清单内。用相似域、异端口、嵌套框架和合法断言验证。
