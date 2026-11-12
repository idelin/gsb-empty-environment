# XML-RPC multicall方法白名单

XML-RPC 服务把 system.multicall 当作一次请求里调用多个方法的入口，攻击者用它夹带未授权的方法，或一次请求塞进几千次登录尝试。为Go的 XML-RPC 服务写可导入的方法门禁包：methodName 精确匹配白名单（区分大小写，Unicode 与空白变体拒绝）；system.multicall 的每个子调用独立过白名单与鉴权，子调用不得再是 system.multicall；子调用数上限 20，超出整体拒绝；失败的子调用返回 fault 结构，其余继续；认证失败的子调用计入同一个速率桶，每个子调用扣 1，不因打包而少扣；system.listMethods 只返回调用者被允许的方法。测嵌套、大小写变体、子调用超限、限速扣减。
