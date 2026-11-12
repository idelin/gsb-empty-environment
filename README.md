# npm标签与内容绑定

镜像服务可把dist-tag指向攻击者版本，或让tarball与元数据不符。为Go包管理代理实现可导入的npm校验包：标签解出版本后，tarball的SRI须匹配（多摘要取最强）；dist.signatures对name@version:integrity的签名经回调通过；包内name、version一致。用换标签、改tarball和错版本样例测试。
