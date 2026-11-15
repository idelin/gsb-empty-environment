// Package netsec 在 ValidatingAdmissionPolicy 与其 Binding 保存前执行失效路径分析。
//
// 分析覆盖四类请求放行条件与一类非法配置：
//   - 参数对象缺失（name 引用不存在）且 parameterNotFoundAction=Allow；
//   - paramRef.selector 命中零个对象且 parameterNotFoundAction=Allow；
//   - failurePolicy=Ignore 且校验表达式或匹配条件静态标记为可能求值出错；
//   - validationActions 不含 Deny，违规请求仅产生 Warn/Audit；
//   - paramRef 同时设置 name 与 selector，配置非法必须拒绝保存。
//
// 此外结合参数资源现有对象与各主体删除授权，标记可被受限主体通过删除参数对象
// 利用的绕过路径（Allow）或可用性风险（Deny 失败关闭）。调用方通过
// Report.Rejected 判断是否拒绝保存，通过 Report.Bypassable 判断是否存在放行绕过。
package netsec
