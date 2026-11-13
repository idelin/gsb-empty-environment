# 反向通道登出令牌校验

攻击者把一个普通 ID 令牌当登出令牌投给服务，或重复投递，想强制他人下线。为Go依赖方写可导入的登出令牌校验包：头部 typ 须为 logout+jwt；events 含 backchannel-logout 事件；sub 与 sid 至少有一个；不得含 nonce；iss、aud 与签名先通过；jti 10 分钟内已见则拒绝；有 sid 只终止该会话，仅有 sub 终止其全部会话。用 ID 令牌冒充、含 nonce、重复 jti 和仅 sub 验证。
