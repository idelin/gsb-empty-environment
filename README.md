# ZooKeeper ACL摘要

ZooKeeper 的 ACL 在创建结点时固定，攻击者利用 auth 方案在未认证时的行为、digest 摘要的构造以及父结点权限去获取更大的权限。为Go的协调服务写可导入的 ACL 求值包：digest 的 ID 为 `用户:Base64(SHA-1(用户:密码))`；创建结点指定 auth 方案时，展开为当前连接已认证的全部身份，连接没有任何已认证身份则返回 InvalidACL；world:anyone 与 ip 方案（IPv4 CIDR）按 READ、WRITE、CREATE、DELETE、ADMIN 权限位判定；删除结点只看父结点的 DELETE 权限，setACL 需要该结点的 ADMIN；ACL 列表为空返回 InvalidACL；配置的超级摘要通过认证即绕过全部检查。测 auth 无身份、父结点删除权、超级摘要、CIDR 边界。
