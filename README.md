# KeyUpdate请求洪泛限额

恶意对端连发 update_requested 的 KeyUpdate，逼本端不停换密钥耗尽 CPU。为Go TLS 1.3 记录层写可导入的密钥更新状态机：收到 update_requested 回且只回一个 update_not_requested，读密钥换为 HKDF-Expand-Label(旧密钥, traffic upd, 空, 哈希长度)，写序号清零；KeyUpdate 不得与其他握手数据同记录或分片；滑动窗口内更新超预算则关连接；认证失败的记录不得推进读代。测洪泛、分片、乱序。
