# IMAP ENABLE编码切换授权一致

IMAP 客户端用 ENABLE UTF8=ACCEPT 把邮箱名从修改版 UTF-7 切换成 UTF-8，攻击者可借两种写法让访问控制与实际邮箱对不上。给Go IMAP 代理写供命令分发调用的扩展协商包，按 RFC 5161：ENABLE 只在已认证且尚未选择邮箱时受理，名字须出现在该连接当前 CAPABILITY 里，未知名字忽略且不出现在 ENABLED 应答中；启用 UTF8=ACCEPT 后邮箱名按 UTF-8 解释，其中的 & 不再是 UTF-7 起始符；授权表按解码后的规范 UTF-8 名保存，两种模式下都要用该规范名判定，不能拿原始字符串比较。用启用前后 caf&AOk- 与 café 的访问、选择邮箱后再 ENABLE、未知扩展名验证。
