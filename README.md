# 联邦信任链与策略收窄

攻击者在联邦里伪造从属声明，想借合法信任链为自己背书。为TypeScript信任方写可导入的 OpenID Federation 信任链校验包：叶子配置自签（iss 等于 sub），其后每条声明由上一级签发、sub 等于下一级 iss，末端是信任锚自签配置；有效期取各 exp 最小值，路径长度不超各级 max_path_length，metadata_policy 只能收窄（下级 subset_of 须是上级子集）。签名验证注入，测断链、超长、放宽策略、过期。
