# Composer安装路径规范化

安装器按包 type 和 extra.installer-name 把包放到项目目录，攻击者给出 ../../ 或绝对路径的 installer-name，或用大小写与保留名让两个包落到同一位置。为Go安装组件实现可导入的安装路径解析包：路径模板只支持 {$vendor}、{$name}、{$type}，名称用小写化后的 vendor/name，installer-name 只许 [a-z0-9._-] 且不得为 . 或 ..；解析后的绝对路径必须仍在项目根内，经过已有符号链接后同样成立；Windows 保留名（CON、NUL、COM1 等）与不同大小写重名者按冲突拒绝。用穿越、符号链接、保留名和大小写冲突验证。
