#include "netsec.hpp"

namespace netsec {

namespace {

// FoE opcode(ETG.1000.4)
constexpr std::uint16_t kFoeOpcodeWriteRequest = 0x02;

// CoE service( CoE 头 bit12-15 )
constexpr std::uint8_t kCoeServiceSdoRequest = 2;
constexpr std::uint8_t kCoeServiceSdoResponse = 3;

std::uint16_t readLe16(const std::uint8_t* p) {
  return static_cast<std::uint16_t>(p[0] | (static_cast<std::uint16_t>(p[1]) << 8));
}

}  // namespace

const char* toString(RejectReason reason) {
  switch (reason) {
    case RejectReason::kNone: return "none";
    case RejectReason::kFrameTooShort: return "frame too short for mailbox header";
    case RejectReason::kLengthExceedsSm: return "length field exceeds sync manager size";
    case RejectReason::kLengthExceedsReceived: return "length field exceeds received bytes";
    case RejectReason::kCounterReserved: return "counter 0 is reserved";
    case RejectReason::kTypeNotAllowed: return "mailbox type not registered by caller";
    case RejectReason::kCoeHeaderTooShort: return "CoE payload too short";
    case RejectReason::kCoeUnexpectedSegment: return "CoE segment without segmented transfer";
    case RejectReason::kCoeToggleMismatch: return "CoE segment toggle bit not alternating";
    case RejectReason::kFoeHeaderTooShort: return "FoE write request header too short";
    case RejectReason::kFoeNameEmpty: return "FoE file name empty";
    case RejectReason::kFoeNameTraversal: return "FoE file name contains path traversal";
  }
  return "unknown";
}

MailboxValidator::MailboxValidator(std::uint16_t sm_size) : sm_size_(sm_size) {}

void MailboxValidator::allowType(std::uint8_t type) {
  allowed_types_[type & 0x0F] = true;
}

void MailboxValidator::disallowType(std::uint8_t type) {
  allowed_types_[type & 0x0F] = false;
}

bool MailboxValidator::parseHeader(const std::uint8_t* data, std::size_t len,
                                   MailboxHeaderView& out) {
  if (data == nullptr || len < kMailboxHeaderSize) return false;
  out.length = readLe16(data);
  out.address = readLe16(data + 2);
  out.channel = data[4] & 0x3F;
  out.priority = (data[4] >> 6) & 0x03;
  out.type = data[5] & 0x0F;
  out.counter = (data[5] >> 4) & 0x07;
  out.reserved = (data[5] >> 7) & 0x01;
  return true;
}

CheckResult MailboxValidator::reject(RejectReason reason) {
  ++reject_count_;
  return {Verdict::kReject, reason};
}

CheckResult MailboxValidator::validate(const std::uint8_t* data, std::size_t len) {
  MailboxHeaderView header{};
  if (!parseHeader(data, len, header)) return reject(RejectReason::kFrameTooShort);

  // 长度字段不得大于邮箱同步管理器大小减 6,也不得大于实际收到字节数减 6。
  // 用 32 位运算避免 sm_size < 6 时无符号回绕。
  if (static_cast<std::uint32_t>(header.length) + kMailboxHeaderSize > sm_size_)
    return reject(RejectReason::kLengthExceedsSm);
  if (static_cast<std::uint32_t>(header.length) + kMailboxHeaderSize > len)
    return reject(RejectReason::kLengthExceedsReceived);

  // 计数器 1..7 循环,0 保留;与上一帧相同视为重传丢弃;跳号只记录不拒绝。
  if (header.counter == 0) return reject(RejectReason::kCounterReserved);
  if (have_last_counter_ && header.counter == last_counter_) {
    ++duplicate_count_;
    return {Verdict::kDropDuplicate, RejectReason::kNone};
  }
  if (have_last_counter_) {
    const std::uint8_t expected = static_cast<std::uint8_t>(last_counter_ % 7 + 1);
    if (header.counter != expected) ++gap_count_;
  }

  if (!allowed_types_[header.type]) return reject(RejectReason::kTypeNotAllowed);

  const std::uint8_t* payload = data + kMailboxHeaderSize;
  CheckResult payload_result{Verdict::kAccept, RejectReason::kNone};
  if (header.type == kMailboxTypeCoE) {
    payload_result = checkCoE(payload, header.length);
  } else if (header.type == kMailboxTypeFoE) {
    payload_result = checkFoE(payload, header.length);
  }
  if (payload_result.verdict == Verdict::kReject) {
    ++reject_count_;
    return payload_result;
  }

  last_counter_ = header.counter;
  have_last_counter_ = true;
  return {Verdict::kAccept, RejectReason::kNone};
}

CheckResult MailboxValidator::checkCoE(const std::uint8_t* payload, std::size_t len) {
  if (len < 2) return reject(RejectReason::kCoeHeaderTooShort);
  const std::uint16_t coe_header = readLe16(payload);
  const std::uint8_t service = (coe_header >> 12) & 0x0F;
  if (service != kCoeServiceSdoRequest && service != kCoeServiceSdoResponse) {
    // 紧急报文、SDO 信息等无段传输语义,不做 toggle 检查。
    return {Verdict::kAccept, RejectReason::kNone};
  }
  if (len < 2 + 4) return reject(RejectReason::kCoeHeaderTooShort);

  const std::uint8_t cmd = payload[2];
  const std::uint8_t cs = cmd >> 5;  // 请求为 ccs,响应为 scs
  bool is_initiate = false;
  bool is_segment = false;
  bool is_abort = false;
  bool has_last_bit = false;
  if (service == kCoeServiceSdoRequest) {
    is_initiate = (cs == 1 || cs == 2);   // 下载/上传初始化请求
    is_segment = (cs == 3 || cs == 4);    // 下载/上传段请求
    is_abort = (cs == 5);
    has_last_bit = (cs == 3);             // 下载段请求 bit0 为最后段标志
  } else {
    is_initiate = (cs == 2 || cs == 3);   // 上传/下载初始化响应
    is_segment = (cs == 0 || cs == 1);    // 上传/下载段响应
    is_abort = (cs == 4);
    has_last_bit = (cs == 0);             // 上传段响应 bit0 为最后段标志
  }

  if (is_abort) {
    coe_segmented_active_ = false;
    return {Verdict::kAccept, RejectReason::kNone};
  }
  if (is_initiate) {
    // bit2 为传输类型:1=加速(单帧),0=分段。加速传输不进入 toggle 跟踪。
    const bool expedited = (cmd & 0x02) != 0;
    coe_segmented_active_ = !expedited;
    coe_expected_toggle_ = 0;
    return {Verdict::kAccept, RejectReason::kNone};
  }
  if (is_segment) {
    if (!coe_segmented_active_) return reject(RejectReason::kCoeUnexpectedSegment);
    const std::uint8_t toggle = (cmd >> 4) & 0x01;
    if (toggle != coe_expected_toggle_) return reject(RejectReason::kCoeToggleMismatch);
    coe_expected_toggle_ ^= 1;
    if (has_last_bit && (cmd & 0x01) != 0) coe_segmented_active_ = false;
    return {Verdict::kAccept, RejectReason::kNone};
  }
  return {Verdict::kAccept, RejectReason::kNone};
}

CheckResult MailboxValidator::checkFoE(const std::uint8_t* payload, std::size_t len) {
  if (len < 2) return reject(RejectReason::kFoeHeaderTooShort);
  const std::uint16_t opcode = readLe16(payload);
  if (opcode != kFoeOpcodeWriteRequest) {
    return {Verdict::kAccept, RejectReason::kNone};
  }
  // 写请求:opcode(2) + password(4) + 文件名(至邮箱数据末尾)。
  if (len < 6) return reject(RejectReason::kFoeHeaderTooShort);
  const std::uint8_t* name = payload + 6;
  const std::size_t name_len = len - 6;
  if (name_len == 0) return reject(RejectReason::kFoeNameEmpty);
  if (!isValidFoeFileName(name, name_len)) return reject(RejectReason::kFoeNameTraversal);
  return {Verdict::kAccept, RejectReason::kNone};
}

bool MailboxValidator::isValidFoeFileName(const std::uint8_t* name, std::size_t len) {
  if (name == nullptr || len == 0) return false;
  for (std::size_t i = 0; i < len; ++i) {
    if (name[i] == 0) return false;
    if (name[i] == '/' || name[i] == '\\') return false;
  }
  // 分隔符已被拒绝,".." 路径段只可能表现为整个文件名就是 ".."。
  if (len == 2 && name[0] == '.' && name[1] == '.') return false;
  return true;
}

}  // namespace netsec
