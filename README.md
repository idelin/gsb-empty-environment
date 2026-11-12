# CocoaPods CDN规范校验

Pod 客户端经 CDN 取 podspec，攻击者替换 CDN 上的 podspec 把 source 指向恶意地址，或在私有源与公共源里发同名 Pod。为Go镜像服务实现可导入的 podspec 校验包：Specs 路径按 MD5(名称) 的前三个十六进制字符各一层目录生成，如 Specs/a/b/c/Name/1.0/Name.podspec.json；podspec.json 的 SHA1 须与 Podfile.lock 的 SPEC CHECKSUMS 一致；Pod 必须来自 lock 中 SPEC REPOS 记录的那个源，同名出现在多个源时以 lock 记录为准而不是 Podfile 的源顺序；source 的 http 地址拒绝，git 源须固定到 40 位提交。用改 source、同名跨源、路径分片和 http 源验证。
