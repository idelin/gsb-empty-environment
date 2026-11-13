#pragma once

#include <cstddef>
#include <cstdint>

namespace netsec {

// EtherCAT 邮箱头固定 6 字节(ETG.1000.4):
//   [0..1] Length   邮箱数据长度(不含本头),小端
//   [2..3] Address  从站地址,小端
//   [4]    Channel(低 6 位) | Priority(高 2 位)
//   [5]    Type(低 4 位) | Counter(bit4-6) | Reserved(bit7)
inline constexpr std::size_t kMailboxHeaderSize = 6;

// 邮箱服务类型
inline constexpr std::uint8_t kMailboxTypeCoE = 3;
inline constexpr std::uint8_t kMailboxTypeFoE = 4;

enum class Verdict {
  kAccept,         // 放行
  kDropDuplicate,  // 重传帧,静默丢弃(非错误)
  kReject,         // 校验失败,拒绝
};

enum class RejectReason {
  kNone = 0,
  kFrameTooShort,         // 不足 6 字节邮箱头
  kLengthExceedsSm,       // 长度字段 > 邮箱同步管理器大小 - 6
  kLengthExceedsReceived, // 长度字段 > 实际收到字节数 - 6
  kCounterReserved,       // 计数器为 0(保留值)
  kTypeNotAllowed,        // 类型未在调用方登记表中
  kCoeHeaderTooShort,     // CoE 数据不足以解析 SDO 命令字节
  kCoeUnexpectedSegment,  // 未处于段传输却收到后续段
  kCoeToggleMismatch,     // CoE 段传输 toggle 位未交替
  kFoeHeaderTooShort,     // FoE 写请求头(opcode+password)不完整
  kFoeNameEmpty,          // FoE 写请求文件名为空
  kFoeNameTraversal,      // FoE 文件名含 '/'、'\\'、".." 段或 NUL 字节
};

struct CheckResult {
  Verdict verdict;
  RejectReason reason;  // 仅当 verdict == kReject 时有意义,否则为 kNone
};

struct MailboxHeaderView {
  std::uint16_t length;    // 邮箱数据长度(不含 6 字节头)
  std::uint16_t address;   // 从站地址
  std::uint8_t channel;    // 低 6 位
  std::uint8_t priority;   // 高 2 位
  std::uint8_t type;       // 低 4 位
  std::uint8_t counter;    // bit4-6,取值 1..7 循环,0 保留
  std::uint8_t reserved;   // bit7
};

const char* toString(RejectReason reason);

// 邮箱帧校验器:按从站会话各持有一个实例,在邮箱读取回调中调用 validate()。
// 校验顺序:帧长 -> 长度字段 -> 计数器 -> 类型登记 -> 负载(CoE/FoE)语义。
// 计数器与 CoE toggle 状态只在帧被放行时推进;被拒绝的帧不污染会话状态。
class MailboxValidator {
 public:
  // sm_size: 调用方邮箱同步管理器的大小(字节,含 6 字节头)。
  explicit MailboxValidator(std::uint16_t sm_size);

  // 调用方登记/注销放行的邮箱类型(如 kMailboxTypeCoE、kMailboxTypeFoE)。
  void allowType(std::uint8_t type);
  void disallowType(std::uint8_t type);

  // data/len 为实际收到的邮箱帧字节(含 6 字节头)。
  CheckResult validate(const std::uint8_t* data, std::size_t len);

  std::uint64_t sequenceGapCount() const { return gap_count_; }  // 跳号次数(只记录不拒绝)
  std::uint64_t duplicateCount() const { return duplicate_count_; }
  std::uint64_t rejectCount() const { return reject_count_; }

  static bool parseHeader(const std::uint8_t* data, std::size_t len,
                          MailboxHeaderView& out);

  // FoE 文件名合法性:非空,不含 '/'、'\\'、NUL 字节及 ".." 路径段。
  static bool isValidFoeFileName(const std::uint8_t* name, std::size_t len);

 private:
  CheckResult reject(RejectReason reason);
  CheckResult checkCoE(const std::uint8_t* payload, std::size_t len);
  CheckResult checkFoE(const std::uint8_t* payload, std::size_t len);

  std::uint16_t sm_size_;
  bool allowed_types_[16] = {};

  bool have_last_counter_ = false;
  std::uint8_t last_counter_ = 0;

  bool coe_segmented_active_ = false;
  std::uint8_t coe_expected_toggle_ = 0;

  std::uint64_t gap_count_ = 0;
  std::uint64_t duplicate_count_ = 0;
  std::uint64_t reject_count_ = 0;
};

}  // namespace netsec
