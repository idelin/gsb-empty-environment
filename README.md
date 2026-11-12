# rsync daemon模块访问控制

rsync daemon 的模块配置限制了来源、路径和选项，攻击者通过模块名大小写、路径参数或被拒选项去读写模块之外的内容。为Go的 rsync 守护服务写可导入的请求准入包：按 rsyncd.conf 的 hosts allow、hosts deny、read only、refuse options、exclude 评估请求，deny 先判，两者都未命中且 allow 存在则拒绝；模块名精确匹配并区分大小写；请求路径相对模块根规范化后不得越出模块根，use chroot 为 no 时，经符号链接指向根外的条目一律拒绝；refuse options 中的选项（含其短选项与 --no- 形式）出现即拒绝；read only 的模块拒绝客户端向它上传。测 allow 与 deny 交叠、短选项、越根路径、只读上传。
