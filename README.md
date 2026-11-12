# I2P LeaseSet时效与旧版本替换

I2P 的 LeaseSet2 告诉别人通过哪些网关到达某个目的地，攻击者可重放旧版本把流量引向已失效或自己控制的网关。给Go netDb 写供存储请求调用的 LeaseSet 准入包，签名验证由调用方注入，以下按本题规定：published 须严格大于已存的同一目的地条目，否则丢弃；published 晚于当前时间 60 秒以上拒绝；expires 偏移不超过 660 秒；每个租约结束时间不得晚于 published 加 expires，租约数 1 至 16，且已过期的租约在返回时被剔除；使用离线签名时，离线签名自己的过期时间不得早于 LeaseSet 的 expires，且不得已过期。用旧版本重放、未来 published、超长 expires、离线签名过早到期验证。
