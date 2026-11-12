# MTA-STS策略缓存与MX通配

攻击者伪造 MTA-STS 策略或剥离 DNS 里的 _mta-sts 记录，想让投递降级为明文。为TypeScript投递组件写可导入的策略缓存：只有经 HTTPS 取得且 id 变化的策略才更新缓存；缓存按 max_age 保留，DNS 记录消失仍沿用至到期；enforce 时 MX 必须匹配策略 mx，通配只认 *.example.com 且只覆盖一个标签，证书名须匹配该 MX；testing 只记录。测多标签通配、MX 仿冒与记录剥离。
