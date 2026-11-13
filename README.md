# kustomization引用与生成器准入

仓库里的 kustomization.yaml 由持续交付系统直接构建，攻击者提交的合并请求可以让 resources 指向仓库外的目录、固定到可变分支的远程地址，或启用会执行外部程序的生成器。请给Go构建网关实现可导入的 kustomization 准入包，输入已解析的配置、仓库根下的文件列表（含符号链接目标）和调用方的远程主机白名单：resources、bases、patches 里的本地路径规范化后必须落在仓库根内，符号链接解析后也要在根内；远程引用必须 https、主机在白名单内，且 ref 是 40 位十六进制提交号，分支、标签和缺省 ref 都拒绝；generators 与 transformers 里 exec 类插件或带 exec 字段的条目一律拒绝；helmCharts 的 repo 须在白名单内并给出 version；configMapGenerator 的 files 条目同样受路径规则约束；循环引用整体拒绝。用 ../ 逃逸、符号链接出根、分支引用、exec 生成器和循环引用验证。
