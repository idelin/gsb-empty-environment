# JARM响应封装

response_mode为jwt时，外层查询参数仍被当成授权结果。结果只取JWT有效载荷；签名密钥、aud、iss、exp和state都要匹配，外层同名参数忽略。查询模式而非jwt模式时按普通参数读取。可导入的Go库供授权服务调用。外层放一个不同的code，JWT里放另一个。
