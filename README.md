# GitHub环境保护主体重拼

环境保护规则只对声明了 environment 的作业生效，攻击者让作业不声明环境，或在环境名里放冒号，凑出与受保护环境相同的 sub 去换发布角色。为Go凭据换发服务实现可导入的角色条件判定包：对已验签的 claims，用 repository 与 environment 重新拼出 repo:<仓库>:environment:<环境>，与 sub 逐字相等才算环境身份；environment 须在角色允许集合内且不含冒号与 *；缺 environment、sub 为 ref: 或 pull_request 形态、event_name 为 pull_request_target 一律拒绝；runner_environment 为 self-hosted 时拒绝标记为仅托管的角色。用冒号环境名、分支触发、缺字段与自托管 runner 验证。
