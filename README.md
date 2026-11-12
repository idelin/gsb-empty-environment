# CIBA通知令牌与重复回调

CIBA 的 ping 模式下，授权服务器用客户端提交的 client_notification_token 回调通知结果，攻击者可冒充服务器伪造通知，或重放旧通知。为Go客户端后端写供通知端点调用的 CIBA ping 处理包：通知须为 POST，Authorization: Bearer 的值必须等于该 auth_req_id 对应的 client_notification_token（常量时间比较），正文 JSON 的 auth_req_id 必须是本客户端发起且尚未终结的请求；通知只表示可以取令牌，收到后用 auth_req_id 访问令牌端点，不得信任通知正文里的任何令牌字段；同一 auth_req_id 的第二次通知忽略；expires_in 到期后的迟到通知拒绝；client_notification_token 长度须在 22 到 1024 字符之间，否则发起请求前即报错。用错误令牌、他人 auth_req_id、重复通知、迟到通知和正文夹带 access_token 验证。
