# SRv6 SRH的HMAC TLV校验

域边界上外部来源的 SRv6 报文若带 SRH，攻击者可借它把流量绕到任意段列表。为Go转发面写可导入的 SRH 准入包（RFC 8754）：来自域外的带 SRH 报文必须带 HMAC TLV 且 Key ID 非 0；HMAC-SHA256 覆盖源地址、Last Entry、Flags、2 字节保留零、Key ID 和整个段列表；Segments Left 大于 Last Entry 加 1 时丢弃。用改段列表、Key ID 为 0、篡改源地址和合法报文验证。
