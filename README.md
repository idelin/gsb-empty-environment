# Gateway API跨命名空间引用授权

共享网关允许各团队的 HTTPRoute 引用后端 Service，攻击者在自己命名空间的路由里引用别人命名空间的 Service，或借 allowedRoutes 的标签选择器把路由挂到不属于自己的监听器。为Go控制器实现可导入的跨命名空间引用解析包：backendRef 指向其他命名空间时，目标命名空间须有 ReferenceGrant，其 from 的 group、kind、namespace 与路由三项全等，to 的 kind 相同且 name 为空或相等；监听器 allowedRoutes 按 Same、All、Selector 判定；撤销 grant 后已解析的引用要立刻失效。用缺授权、kind 不符、name 通配和撤销顺序验证。
