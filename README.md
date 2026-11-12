# crate审计增量链与违规区间

依赖门禁用审计记录判断 crate 版本能否部署，攻击者发布的版本落在 violation 区间，或借 delta 审计链接到一个未被完整审计的起点。为Go门禁实现可导入的审计链解析包：完整审计给出 version，增量审计给出 from->to；一个版本通过当且仅当存在从某完整审计出发、沿同一 criteria 的增量边可达它的路径；violation 的 semver 区间内的任何版本一律不通过，且 violation 优先于审计；safe-to-deploy 蕴含 safe-to-run，反向不成立；预发布版本按 semver 排序，+ 构建元数据忽略。用环、断链、区间边界和 criteria 蕴含验证。
