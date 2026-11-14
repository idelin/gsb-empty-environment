# 工作负载证书的SAN通配放大了服务身份

服务网格按证书SAN里的`*.prod`授予生产身份，攻击者用`evil.prod`的测试证书取得生产mTLS身份。请实现供Go服务网格控制面调用的可导入工作负载身份验证组件。身份由trust domain、SPIFFE路径、SAN集合、签名链和节点证明共同决定；通配只允许出现在显式声明的最左标签，深度与主体用途受限，IP SAN、URI SAN和DNS SAN不得互相替代。证书吊销、short-lived轮换和bundle更新按代次生效，调用方与目标身份策略在每次连接建立时评估。用`evil.prod`、多级通配、URI冒充、跨trust domain、吊销后连接和合法同域服务验证身份不被放大。
