# 遗留Pin与Expect-CT头忽略

站点被入侵后，攻击者给响应加上 Public-Key-Pins 或 Expect-CT，企图让浏览器长期锁死到攻击者控制的密钥，或把证书错误报告发往他的地址。为TypeScript网络栈写可导入的遗留头策略包：这两个头不再改变任何状态（不存储 pin、不开启 CT 强制、不向 report-uri 发请求）；升级时磁盘上遗留的动态 pin 记录在启动时清除，之后不参与证书判定；调用方编译进来的静态 pin 仍对对应主机强制，是否对用户安装的根证书豁免由配置项决定，默认不豁免；Public-Key-Pins-Report-Only 同样不产生报告；同一响应里的 Strict-Transport-Security 处理不受这两个头影响。测动态 pin 设置、旧记录清除、静态 pin 强制、report-uri 外发。
