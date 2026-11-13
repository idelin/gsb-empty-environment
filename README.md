# SNMPv3 USM时间窗与发现

SNMPv3 的用户安全模型靠 engineID、engineBoots 与 engineTime 防重放，攻击者重放旧报文，或伪造发现响应让管理端同步到错误的时间。为Go SNMP 代理写可导入的 USM 时间窗包，供报文入口调用：本地 engineBoots 与 engineTime 由调用方给出；认证通过（调用方回调）之前不得根据报文里的值更新本地同步状态；报文 engineBoots 与本地不同，或 engineTime 与本地相差超过 150 秒，返回 notInTimeWindow 报告；本地 engineBoots 为 2147483647 时拒绝所有认证报文；未知 engineID 的发现请求只可回复 unknownEngineID 报告，不泄露用户是否存在；本题为防用户枚举规定：用户名不存在与认证失败返回完全相同的报告。用 149 与 151 秒边界、重放旧 boots、未认证更新同步、用户枚举和 boots 上限验证。
