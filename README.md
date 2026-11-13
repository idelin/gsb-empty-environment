# 转发意图的URI授权范围核对

应用把收到的意图转发给内部组件前，往往只检查 data 里的地址，攻击者把受保护的内容地址放进 ClipData，再带上读取授权标志，授权对 ClipData 里的每一项同样生效。给Go移动应用审查服务实现可导入的转发意图判定包，输入被转发意图的标志位、data、ClipData 条目地址列表、extras 里嵌套的子意图（可再嵌套）和调用方的可授权 authority 白名单：标志位含 0x1（读）、0x2（写）或 0x80（前缀）时，data、全部 ClipData 条目以及所有嵌套子意图里的地址，其 authority 都必须在白名单内，否则必须清除授权标志后才可转发；authority 取 :// 之后、第一个 / 之前、最后一个 @ 之后的部分并转小写，去掉端口，用户信息段不得参与判定（content://app.safe@evil/x 的 authority 是 evil）；content 之外的 scheme 不要求白名单，但 file 一律拒绝；嵌套超过 3 层拒绝。用 ClipData 夹带、用户信息伪装、嵌套子意图、file 协议和清除标志后转发验证。
