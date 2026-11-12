# AppImage更新签名与节区

AppImage 内嵌 .upd_info 指向更新源，签名在 .sha256_sig，公钥在 .sig_key，攻击者在新文件里换上自己的公钥与签名。为Go更新组件实现可导入的更新判定包：解析 ELF64 节头找到这三个节，节偏移加大小越界或节互相重叠即拒；摘要为文件中 .sha256_sig 与 .sig_key 两节置零后的 SHA-256；验签只用当前运行镜像里的公钥，新文件自带的公钥忽略；gh-releases-zsync|所有者|仓库|latest|文件名通配 中的通配按整串匹配且不跨 /，下载与 zsync 地址只许 https。用换钥、重叠节、越界节与 http 地址验证。
