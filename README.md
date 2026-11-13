# Matrix脱敏事件授权与到达顺序

Matrix 联邦里的 m.room.redaction 会清除目标事件内容，攻击者所在服务器可伪造对他人事件的脱敏，或让脱敏先于目标到达来绕过检查。给Go主服务器写供联邦 PDU 入口调用的脱敏包，以下按本题规定：脱敏事件在其发送者拥有房间 redact 权限等级，或其发送者所在服务器与目标事件发送者所在服务器相同时才授权；目标尚未到达时脱敏进入待决表，目标到达后再按脱敏事件当时的房间状态判定，上限每房间 1000 条；脱敏只保留 event_id、type、room_id、sender、state_key 以及 content 中该类型的白名单键（m.room.member 留 membership，m.room.power_levels 留 ban、kick、redact、events、users 等），其余清空。用跨服务器越权脱敏、先脱敏后到达、待决表上限、被脱敏的 power_levels 验证。
