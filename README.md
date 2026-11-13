# 清单保护声明密钥一致性

攻击者改写 DASH 的 MPD，让 default_KID 与初始化分段 tenc、PSSH 里的密钥标识不一致，诱使播放器向别的密钥申请许可。为TypeScript播放器写可导入的校验包：同一 AdaptationSet 各 Representation 的 default_KID 须一致且等于 tenc 的 KID（忽略连字符与大小写），方案值同 tenc，PSSH 含该 KID，licenseUrl 为白名单内 https。
