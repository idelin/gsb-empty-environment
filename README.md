# FIX会话序号重放守卫

攻击者重发FIX旧报文，或在Logon里带ResetSeqNumFlag(141)=Y清零序号。给Go接入网关写供会话读取循环调用的序号守卫：MsgSeqNum(34)偏小且PossDupFlag(43)不为Y就断开；为Y时OrigSendingTime(122)不得晚于SendingTime(52)；跳号触发ResendRequest；重置仅在调用方允许时接受。用旧报文、跳号和非法重置验证。
