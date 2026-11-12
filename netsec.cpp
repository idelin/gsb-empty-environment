#include "netsec.hpp"

#include <cstring>
#include <set>
#include <string>

namespace netsec {

const char* toString(BundleError error) noexcept {
    switch (error) {
        case BundleError::kOk: return "ok";
        case BundleError::kByteBudgetExceeded: return "byte budget exceeded";
        case BundleError::kEntryBudgetExceeded: return "entry budget exceeded";
        case BundleError::kDepthExceeded: return "nesting depth exceeded";
        case BundleError::kTruncated: return "declared length exceeds remaining bytes";
        case BundleError::kTrailingBytes: return "trailing bytes after declared bundle";
        case BundleError::kBadMagic: return "bad magic";
        case BundleError::kLengthMismatch: return "length mismatch";
        case BundleError::kDuplicateKey: return "duplicate key";
        case BundleError::kUnknownValueType: return "unknown value type";
    }
    return "unknown error";
}

namespace {

constexpr std::size_t kHeaderSize = 8;  // total_len:u32 + magic:"BNDL"
constexpr char kMagic[4] = {'B', 'N', 'D', 'L'};

class Parser {
public:
    Parser(const std::uint8_t* data, std::size_t size, const BundleValidationOptions& options)
        : data_(data), size_(size), options_(options) {}

    BundleValidationResult run() {
        if (size_ > options_.maxTotalBytes) {
            return fail(BundleError::kByteBudgetExceeded, 0);
        }
        std::size_t cursor = 0;
        std::uint32_t totalLen = 0;
        if (!readU32(cursor, size_, totalLen)) {
            return fail(BundleError::kTruncated, cursor);
        }
        if (totalLen < kHeaderSize) {
            return fail(BundleError::kLengthMismatch, 0);
        }
        if (totalLen > size_) {  // 声明长度超过剩余字节
            return fail(BundleError::kTruncated, 0);
        }
        if (totalLen < size_) {  // 声明总长与输入不符：存在残余字节
            return fail(BundleError::kTrailingBytes, totalLen);
        }
        return parseBody(cursor, size_, /*depth=*/1);
    }

private:
    static BundleValidationResult ok(std::size_t offset) {
        return BundleValidationResult{BundleError::kOk, offset};
    }

    static BundleValidationResult fail(BundleError error, std::size_t offset) {
        return BundleValidationResult{error, offset};
    }

    bool readU8(std::size_t& cursor, std::size_t limit, std::uint8_t& out) const {
        if (limit - cursor < 1) return false;
        out = data_[cursor];
        cursor += 1;
        return true;
    }

    bool readU32(std::size_t& cursor, std::size_t limit, std::uint32_t& out) const {
        if (limit - cursor < 4) return false;
        out = static_cast<std::uint32_t>(data_[cursor]) |
              (static_cast<std::uint32_t>(data_[cursor + 1]) << 8) |
              (static_cast<std::uint32_t>(data_[cursor + 2]) << 16) |
              (static_cast<std::uint32_t>(data_[cursor + 3]) << 24);
        cursor += 4;
        return true;
    }

    // 解析 [cursor, bundleEnd) 内的 magic 与条目；所有读取均以 bundleEnd 为界，
    // 因此逐字段解析的结束位置恰好等于声明的 bundleEnd，二者不一致即拒绝。
    BundleValidationResult parseBody(std::size_t cursor, std::size_t bundleEnd, std::size_t depth) {
        if (bundleEnd - cursor < 4) {
            return fail(BundleError::kTruncated, cursor);
        }
        if (std::memcmp(data_ + cursor, kMagic, 4) != 0) {
            return fail(BundleError::kBadMagic, cursor);
        }
        cursor += 4;

        std::set<std::string> keys;
        while (cursor < bundleEnd) {
            if (++entriesSeen_ > options_.maxEntries) {
                return fail(BundleError::kEntryBudgetExceeded, cursor);
            }

            std::uint32_t keyLen = 0;
            if (!readU32(cursor, bundleEnd, keyLen) || keyLen > bundleEnd - cursor) {
                return fail(BundleError::kTruncated, cursor);
            }
            const std::size_t keyOffset = cursor;
            std::string key(reinterpret_cast<const char*>(data_ + cursor), keyLen);
            cursor += keyLen;
            if (!keys.insert(std::move(key)).second) {
                return fail(BundleError::kDuplicateKey, keyOffset);
            }

            std::uint8_t type = 0;
            if (!readU8(cursor, bundleEnd, type)) {
                return fail(BundleError::kTruncated, cursor);
            }
            switch (type) {
                case 1: {  // 4 字节整数
                    if (bundleEnd - cursor < 4) {
                        return fail(BundleError::kTruncated, cursor);
                    }
                    cursor += 4;
                    break;
                }
                case 2: {  // 长度前缀字符串
                    std::uint32_t len = 0;
                    if (!readU32(cursor, bundleEnd, len) || len > bundleEnd - cursor) {
                        return fail(BundleError::kTruncated, cursor);
                    }
                    cursor += len;
                    break;
                }
                case 3: {  // 长度前缀嵌套 Bundle
                    std::uint32_t len = 0;
                    if (!readU32(cursor, bundleEnd, len) || len > bundleEnd - cursor) {
                        return fail(BundleError::kTruncated, cursor);
                    }
                    if (depth + 1 > options_.maxDepth) {
                        return fail(BundleError::kDepthExceeded, cursor);
                    }
                    // 嵌套 Bundle 的内部声明总长必须与外层声明的字节数一致
                    const std::size_t nestedStart = cursor;
                    std::uint32_t innerTotal = 0;
                    std::size_t peek = cursor;
                    if (len < kHeaderSize || !readU32(peek, nestedStart + len, innerTotal) ||
                        innerTotal != len) {
                        return fail(BundleError::kLengthMismatch, cursor);
                    }
                    BundleValidationResult result =
                        parseBody(nestedStart + 4, nestedStart + len, depth + 1);
                    if (!result.ok()) {
                        return result;
                    }
                    cursor = nestedStart + len;
                    break;
                }
                default:
                    return fail(BundleError::kUnknownValueType, cursor - 1);
            }
        }
        return ok(cursor);
    }

    const std::uint8_t* data_;
    std::size_t size_;
    const BundleValidationOptions& options_;
    std::size_t entriesSeen_ = 0;
};

}  // namespace

BundleValidationResult validateBundle(const void* data, std::size_t size,
                                      const BundleValidationOptions& options) {
    if (data == nullptr) {
        size = 0;
    }
    Parser parser(static_cast<const std::uint8_t*>(data), size, options);
    return parser.run();
}

}  // namespace netsec
