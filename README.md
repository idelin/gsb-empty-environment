# 103提示与终态CSP对齐

浏览器按 103 里的 Link: rel=preload 提前抓取资源，而终态响应的 CSP 可能更严；若仍复用提前抓取的内容，由中间节点注入的 103 就能绕过终态策略。为TypeScript浏览器网络栈写可导入的预加载复用判定包：每个提示条目记录 URL、as 类型、crossorigin 模式；终态响应到达后，仅当终态 CSP 对该 URL 与 as 对应的指令（script-src、style-src、img-src、font-src）放行、条目的 crossorigin 模式与页面标签请求一致、条目的完整性元数据与标签相同才复用，否则丢弃并重新抓取；终态为重定向或非 2xx 时清空全部条目；条目超过 30 秒未被使用也丢弃。用 CSP 拒绝、crossorigin 不一致、完整性不一致、重定向终态和过期验证。
