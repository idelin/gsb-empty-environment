# ARC封印链实例连续性

攻击者在转发途中插入、删除或调换 ARC 集，想让断链显示为通过。为Go收件端写可导入的 ARC 链校验包：实例号须从1连续到N，每个 i 恰有 AS、AMS、AAR 各一；i=1 的 AS 其 cv 只能是 none，i>1 只能是 pass 或 fail；任一 cv=fail 或最新 AMS 验签失败则整链 fail；AS 签名覆盖 i 及以下全部 ARC 头。验签函数由调用方注入，测缺号、重复、cv 错位和调换。
