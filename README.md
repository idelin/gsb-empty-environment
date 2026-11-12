# NetworkPolicy命名空间选择交并

多租户集群用 NetworkPolicy 隔离命名空间，攻击者给自己的命名空间打上 team=payments 这类可自改标签，或利用 from 里 namespaceSelector 与 podSelector 写成同一项还是两项的差别。为Go控制器实现可导入的入站放行判定库：同一 from 元素内的 namespaceSelector 与 podSelector 取交集，不同元素取并集，空选择器 {} 选中全部，仅有 podSelector 时限定在策略所在命名空间；输入里 kubernetes.io/metadata.name 之外的命名空间标签视为租户可改，凡放行依赖可改标签时都在结果中标出。用交集与并集写法、空选择器和可改标签命名空间验证。
