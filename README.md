# 802.1X端口授权模式与搭车MAC违规

端口上先有一台合法设备通过认证，后面再接集线器挂上另一台，就可能搭着已授权状态通信。为Go交换机接入组件写可导入的端口授权判定包：single-host 只授权首个认证的 MAC，其他源 MAC 的帧触发违规动作（restrict 丢弃，shutdown 禁用端口）；multi-auth 每个 MAC 单独认证，未认证 MAC 的帧丢弃；multi-host 认证一个后放行所有，首个 MAC 老化或 EAPOL-Logoff 后整端口回到未授权。用第二个 MAC、logoff 和老化验证。
