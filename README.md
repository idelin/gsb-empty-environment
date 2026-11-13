# 受限令牌把deny-only组当成授权组

沙箱服务从高权限进程令牌派生受限令牌，若访问检查仍把deny-only组用于允许ACE，就会恢复被剥离的权限。给Go宿主实现题内Windows式令牌与访问检查包：令牌含用户SID、启用组、deny-only组、禁用组、启用特权、完整性级别及受限SID集合；派生操作只能把启用组移到deny-only或禁用、删除特权、降低完整性并追加受限SID，不能加入新身份。对象安全描述符含有序ACE，每项为Allow或Deny、SID和权限位；访问检查先把用户与启用组用于Allow匹配，把用户、启用组及deny-only组用于Deny匹配，禁用组不匹配。先按顺序遇到匹配Deny立即拒绝，匹配Allow累积已准位；按顺序处理Deny/Allow并要求请求位全部获准；若受限SID集合非空，还须以“用户仅替换为受限SID集合、deny-only仍只拒绝”的第二次检查同样通过，两次结果取交。对象完整性标签高于令牌时禁止Write与ChangeOwner，即使DACL允许；启用特权仅对题面明确的BackupRead或TakeOwnership位生效。实现派生、访问判定及逐ACE证据，枚举小SID/权限域对照集合参考，覆盖deny-only Allow、受限SID双检查、显式Deny顺序、完整性写入和已删除特权。

## API 摘要

- `Token`：`UserSID`、`EnabledGroups`、`DenyOnlyGroups`、`DisabledGroups`、`EnabledPrivileges`、`IntegrityLevel`、`RestrictedSIDs`。
- `Token.DeriveRestricted` / `DeriveRestricted`：接受 `RestrictOptions` 或位置参数，只能收紧令牌。
- `CheckAccess` / `AccessCheck`：返回 `Decision`，包含 `Allowed`、`Granted` 以及 `Primary`/`Restricted` 两轮检查的逐 ACE `Evidence`。
- 权限位：`AccessRead`、`AccessWrite`、`AccessExecute`、`AccessChangeOwner`、`AccessBackupRead`、`AccessTakeOwnership`。
- 完整性级别：`IntegrityUntrusted`、`IntegrityLow`、`IntegrityMedium`、`IntegrityHigh`、`IntegritySystem`。
- 特权：只有 `SeBackupPrivilege` 对应 `AccessBackupRead`，`SeTakeOwnershipPrivilege` 对应 `AccessTakeOwnership`。
