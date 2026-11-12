# Elasticsearch查询体脚本门禁

攻击者在查询体深层夹带 inline 脚本，想绕过只许存储脚本的策略。为Go搜索网关写可导入的脚本门禁：递归检查任意位置的 script、script_score、runtime_mappings 字段脚本、scripted_metric 的四个 *_script 与 bucket_script、bucket_selector；只放行 {id, params} 的存储脚本，带 source、inline、lang 或写成字符串的拒绝；JSON 重复键拒绝。测嵌套聚合、运行时字段、重复键。
