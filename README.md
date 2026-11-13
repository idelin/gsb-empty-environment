# 项目级.npmrc覆盖registry与凭据外发

仓库里的 .npmrc 把 registry 指向攻击者主机，并用环境变量展开把用户的发布令牌一起带过去。为TypeScript包管理器封装写可导入的配置合并判定包：项目文件里的认证行（//host/:_authToken）主机须与调用方登记的 registry 主机一致；环境变量展开只允许白名单变量，且仅发往登记主机；项目文件设置 script-shell、git 这类可执行路径键一律拒绝；作用域 registry 指向未登记主机拒绝。用外发令牌、非白名单变量、script-shell 和合法配置验证。
