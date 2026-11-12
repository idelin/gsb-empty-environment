# Diameter AVP必需位处理

Diameter对端可把带M位的未知AVP伪装成可忽略项，或让嵌套AVP超出父长度，来绕过计费和授权约束。为Go AAA代理实现AVP解码包，供转发前调用：长度含头并四字节填充，V位与Vendor-Id一致，Grouped嵌套不超过8层且不越过父边界；未知且置M位的AVP返回5001并带Failed-AVP，未知非必需AVP保留转发。用越界、清除M位和合法报文验证。
