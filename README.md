# ZFS委托权限范围判定

ZFS 的 zfs allow 能把创建、挂载、快照、发送等权限委托给普通用户，范围可以是数据集本身、后代或二者，攻击者想借 mount 与 create 的组合得到 setuid 文件，或借 rename 与 sharenfs 逃出委托范围。为 Go 存储管理服务写供委托授权判定调用的包：输入数据集树、委托记录（主体：用户、组或 everyone；权限名；范围：本数据集、后代、二者）和请求（主体、操作、目标数据集、涉及的属性）；委托沿祖先叠加，仅限本数据集的记录不对后代生效；修改 mountpoint、sharenfs、canmount、setuid 需同时持有 mount 与相应属性权限；rename 须在源与目标父数据集都有 rename 与 mount（本题规定）；创建数据集时指定 setuid=on 而调用者无 setuid 委托则拒绝。用仅本数据集委托、跨父重命名、setuid 属性和合规操作验证。
