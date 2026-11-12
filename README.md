# libvirt磁盘密钥引用归属校验

libvirt 用 secret 对象向虚拟机磁盘与网络配置注入密码、Ceph 密钥或 LUKS 口令，域 XML 里引用的 secret 由租户填写，攻击者想引用别的租户的 secret，或用 usage 名称而不是 UUID 绕过归属检查。为 Go 管理面写供域定义与启动前调用的 secret 引用校验包：输入域 XML 片段（磁盘的认证与加密元素里的 secret：type、uuid 或 usage）、secret 注册表（uuid、usage、所属租户、private 与 ephemeral 标志）及租户身份；只允许引用本租户的 secret；按 usage 引用时若匹配多个，只考虑本租户的并要求无歧义；private 的 secret 值不得出现在任何查询结果；ephemeral 的 secret 不落盘且管理面重启后不可用；域的 UUID 与 secret 的 UUID 相同不得混淆。用引用他人 UUID、usage 歧义、private 读取和合规引用验证。
