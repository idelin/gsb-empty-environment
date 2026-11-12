# gRPC-Web尾部帧防伪

攻击者控制的上游元数据可含CRLF，借gRPC-Web尾部帧伪造grpc-status。为Go代理实现可导入的响应转换包：trailer帧标志0x80，内容为小写键“key: value\r\n”；键值含控制字符或保留grpc-前缀的自定义项拒绝，grpc-status只取真实HTTP/2 trailers。用CRLF键值和缺失trailers测试。
