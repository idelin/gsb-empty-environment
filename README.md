# gem签名剥离与重名成员

gem 的 tar 里有 metadata.gz、data.tar.gz、checksums.yaml.gz 及对应 .sig，攻击者剥掉签名让低安全策略放行，或放两份同名 data.tar.gz 使校验与解包读到不同内容。为Go镜像服务实现可导入的 gem 校验包：成员名重复即拒；HighSecurity 要求 metadata 与 data 都有有效 RSA-SHA256 签名且证书链终止于信任根，MediumSecurity 对已签名者校验但允许整体未签名，只剥掉其中一个 .sig 一律拒绝；checksums.yaml 里的 SHA512 须与实际成员一致。用剥签名、重名成员、半签和错链验证。
