# transportsim

按包序号维护发送、ACK、重传与超时的传输模拟器。

## 运行

    go run . events.txt        # 或: cat events.txt | go run .
    go test ./...

## 输入事件（每行一条，`#` 为注释）

    t=<time> send N        # 发送至多 N 个新包（受 cwnd 限制，超出记 BLOCKED）
    t=<time> ack SEQ       # ACK 到达；是否重复由包状态判定，与标签无关
    t=<time> dupack SEQ    # 显式重复 ACK
    t=<time> drop SEQ      # 网络丢包（发送方不知情）
    t=<time> timeout SEQ   # RTO 超时：重传 + cwnd=1
    t=<time> tick          # 仅推进时间

## 核心规则

- 每个包占恰好 1 单位在途额度；重传只增加物理发送次数，不增加逻辑在途包数。
- 重复 ACK 不重复释放额度、不改变窗口；连续 3 个触发对最老未确认包的快速重传。
- 重传包的 ACK 不用于 RTT 采样（Karn 算法），srtt/rttvar/rto 只由首次传输的 ACK 更新。
- 窗口：新 ACK 时 cwnd < ssthresh 则 +1（慢启动），否则 +1/cwnd（拥塞避免）；
  超时 ssthresh=max(cwnd/2,2)、cwnd=1；快速重传 ssthresh=max(cwnd/2,2)、cwnd=ssthresh。

## 核对

- 守恒式（每个事件后检查）：sent == acked + inflight；creditReleased == acked；
  每个包 acked <=> released（额度恰好释放一次）。
- 丢包重放审计：丢包/超时后必须先重传才能被 ACK；重传后不得再有 RTT 采样；
  结束时仍未修复的丢包以 note 报告。
- 确定性重放：同一事件日志重跑一遍，轨迹必须逐字节一致。
