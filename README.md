# MTA-STS策略缓存与MX通配

攻击者伪造 MTA-STS 策略或剥离 DNS 里的 _mta-sts 记录，想让投递降级为明文。为TypeScript投递组件写可导入的策略缓存：策略文档须 200 且 content-type 为 text/plain，max_age 有上下界，mode 仅 enforce/testing/none；只有经 HTTPS 取得且 DNS 记录 id 与文档 id 一致时才更新缓存，id 变化时旧策略立即失效；缓存按域名与 id 分开保留，DNS 记录消失仍沿用至到期；enforce 时 MX 必须匹配策略 mx，通配只认 *.example.com 且只覆盖一个标签，证书名须匹配该 MX，无可用 MX 或证书不符即拒绝投递而不是降级；testing 只记录。测多标签通配、MX 仿冒、记录剥离、id 不一致、非法 max_age 与旧缓存复用。
