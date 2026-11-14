# SSH证书的强制命令在复用会话后失效

堡垒机用SSH CA签发带`force-command`的证书，但它只在首次认证时执行，连接复用或重新协商后命令约束被丢弃，攻击者可执行任意命令。请实现供C++20 SSH服务端调用的可导入证书约束组件。解析证书的critical options与extensions，未知critical选项即拒绝；`force-command`、`source-address`、`permit-open`等约束绑定会话生命周期，每次通道打开与重新协商都重新评估。证书CA、主体、有效期与revocation按代次核对，主机密钥变化不改变用户授权。用命令替换、端口转发、多路复用、重新协商、未知选项、合法受限命令和撤销后连接验证约束在整条会话内有效。
