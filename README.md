# 部分IV长度

partial IV短于算法要求时左侧补零后与另一条消息碰撞。partial IV的长度必须符合该算法，不补零；长度不对则失败。长度正确的partial IV可以参与随机数构造。可导入的Go库供验证组件调用。提交短一个字节的partial IV。
