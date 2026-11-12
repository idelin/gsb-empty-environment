# OPA数据根归属绑定

攻击者提交的策略包会把数据文档写进其他团队的路径。为Go策略服务实现可导入的bundle加载包：.manifest的roots按路径段前缀判断（a/b不含a/bc），文件须在自身roots内，多个bundle的roots不得相交；sha256须与清单一致，缺失或多余即整体拒绝。用段前缀、空root和重叠bundle测试，失败时旧版本继续生效。
