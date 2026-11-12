# purl规范化与策略匹配

依赖风险策略按 purl 匹配被禁组件，攻击者发布大小写、编码或限定符顺序不同的同名包来逃过精确匹配。为Go策略服务实现供 SBOM 入库调用的 purl 规范化与条件匹配包：type 转小写，github 与 bitbucket 的 namespace 和 name 转小写，maven 保持大小写；先百分号解码再比较，qualifiers 按键排序，subpath 不参与匹配；策略条件可用 * 通配并按 ALL 或 ANY 聚合，违规级别 FAIL 高于 WARN 高于 INFO，组件取其命中中的最高级别。用大小写变体、%2F 编码、限定符顺序、maven 大小写区别和未命中验证。
