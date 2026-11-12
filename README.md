# open_tree与move_mount序列校验

新挂载接口允许先 open_tree 克隆一棵挂载树，再 move_mount 挂到目标位置。攻击者用 OPEN_TREE_CLONE 加 AT_RECURSIVE 克隆宿主私有挂载点，或在两次调用之间把目标路径换成符号链接。为 Go 容器运行时写供调用序列校验的包：输入事件序列（open_tree 的路径与标志、mount_setattr 的传播类型、move_mount 的源描述符与目标路径及标志、目标路径在检查时与使用时各自解析到的 inode 标识）；克隆路径落在调用方给出的敏感前缀下拒绝；move_mount 的源描述符须由本序列内的 open_tree 克隆或 fsmount 产生且只用一次；目标路径检查与使用时解析出的 inode 不同则拒绝；递归克隆带入 shared 传播的子树，除非先改为 private 或 slave，否则拒绝；描述符被关闭后再使用拒绝。用敏感路径克隆、目标被替换、重复使用描述符和 shared 子树验证。
