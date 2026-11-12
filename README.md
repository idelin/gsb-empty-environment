# 锁定文件别名与OSV区间闭合

扫描器按锁定文件里的包名版本查漏洞，攻击者在 package-lock 里用别名让显示名与真实包不一致，或让 resolved 指向非官方仓库。为TypeScript扫描服务实现供发布门禁调用的锁定文件闭合包：lockfileVersion 3 的 packages 条目取真实名称（name 字段优先，其次路径中最后一个 node_modules 段），resolved 主机不在允许表即报告；对 OSV 的 SEMVER 区间事件（introduced、fixed、last_affected，同一范围按序评估，预发布版本小于对应正式版）判定是否受影响，且每个被锁定的包版本都必须有评估结果，缺失则整体不闭合。用别名包、introduced 为 0、fixed 边界、last_affected 和未评估条目验证。
