# 镜像仓库令牌作用域授权求交

镜像仓库的令牌服务把请求里的 scope 和用户权限求交后签发令牌，攻击者在 scope 里塞多个动作、拼路径段或写通配，企图拿到本不该有的 push 权限。为Go令牌服务写可导入的 scope 授权包，scope 形如 repository:命名空间/名称:动作1,动作2，一次请求可带多个 scope 参数；调用方给出用户权限表（仓库名前缀到允许动作，如 team-a/* 允许 pull,push）：仓库名按小写字母数字、. _ - 与 / 分段校验，含 ..、空段或大写时整条丢弃；动作只认 pull、push、delete，出现其他动作或 * 时整条丢弃；同一仓库的多个 scope 先取动作并集，再与权限求交；授予 push 或 delete 必须同时拥有 pull；registry:catalog:* 仅对有列表权限的用户授予；求交为空的 scope 仍出现在令牌里并带空动作集，不得省略。用 team-a/../team-b、* 动作、重复 scope 合并、push 无 pull 和空交集验证。
