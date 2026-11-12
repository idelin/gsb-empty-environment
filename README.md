# S4U2Proxy委派判定

被攻陷的服务想借约束委派，以任意用户身份访问未授权的后端。为Go KDC组件写可导入的 S4U2Proxy 判定包：附加票据须 forwardable；用户账号标为不可委派或属受保护用户组则拒绝；目标 SPN 须在服务账号的 AllowedToDelegateTo 列表，或后端 RBCD 描述符列出该服务；S4U2Self 所得票据仅在协议转换启用时才可 forwardable；PAC 里的客户端信息须与票据一致。测不可委派用户、列表外 SPN、不可转发票据、PAC 不符。
