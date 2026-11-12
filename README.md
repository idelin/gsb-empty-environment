# JSON-LD上下文固定策略

攻击者在可验证凭证里引用可控的远程 @context，换掉术语定义使签名内容含义改变。为TypeScript验证库写可导入的上下文加载策略：只从调用方登记的（URL，内容摘要）表读取 context，未登记即拒绝而不发起网络请求；受保护术语（protected: true）不得被后续 context 或凭证内联重定义；@context 首元素须是基础凭证上下文；对内联 context 做同样检查。测未登记 URL、术语重定义、上下文顺序。
