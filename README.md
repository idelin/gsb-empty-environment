# File System Access权限收缩

攻击者想把已授权目录句柄的权限扩张到父目录或其他来源。为TypeScript浏览器宿主实现可导入的File System Access权限模型：子句柄权限不超过父句柄，撤销父权限后已派生子句柄变为prompt；getFileHandle名称不得含路径分隔符或..；句柄不得跨来源克隆；持久化授权重启后只恢复读权限，写权限须再确认。用撤销、..名和降级样例测试。
