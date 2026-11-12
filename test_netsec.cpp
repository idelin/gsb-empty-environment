#include "netsec.hpp"

#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

namespace {

int failures = 0;

#define CHECK(cond)                                                     \
    do {                                                                \
        if (!(cond)) {                                                  \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << ": " \
                      << #cond << std::endl;                            \
            ++failures;                                                 \
        }                                                               \
    } while (0)

class Builder {
public:
    Builder& u8(std::uint8_t v) {
        buf_.push_back(v);
        return *this;
    }

    Builder& u32(std::uint32_t v) {
        for (int i = 0; i < 4; ++i) {
            buf_.push_back(static_cast<std::uint8_t>((v >> (8 * i)) & 0xff));
        }
        return *this;
    }

    Builder& bytes(const void* p, std::size_t n) {
        const auto* b = static_cast<const std::uint8_t*>(p);
        buf_.insert(buf_.end(), b, b + n);
        return *this;
    }

    Builder& bytes(const std::vector<std::uint8_t>& v) { return bytes(v.data(), v.size()); }

    const std::vector<std::uint8_t>& buf() const { return buf_; }

private:
    std::vector<std::uint8_t> buf_;
};

// body := entry*，自动补上 total_len 与魔数
std::vector<std::uint8_t> makeBundle(const std::vector<std::uint8_t>& body) {
    Builder b;
    b.u32(static_cast<std::uint32_t>(body.size() + 8));
    b.bytes("BNDL", 4);
    b.bytes(body);
    return b.buf();
}

void putKey(Builder& b, const std::string& key) {
    b.u32(static_cast<std::uint32_t>(key.size()));
    b.bytes(key.data(), key.size());
}

void putInt(Builder& b, const std::string& key, std::uint32_t value) {
    putKey(b, key);
    b.u8(1).u32(value);
}

void putString(Builder& b, const std::string& key, const std::string& value) {
    putKey(b, key);
    b.u8(2).u32(static_cast<std::uint32_t>(value.size()));
    b.bytes(value.data(), value.size());
}

void putNested(Builder& b, const std::string& key, const std::vector<std::uint8_t>& nested) {
    putKey(b, key);
    b.u8(3).u32(static_cast<std::uint32_t>(nested.size()));
    b.bytes(nested);
}

netsec::BundleValidationResult validate(const std::vector<std::uint8_t>& blob,
                                        const netsec::BundleValidationOptions& options = {}) {
    return netsec::validateBundle(blob.data(), blob.size(), options);
}

void testValidFlat() {
    Builder body;
    putInt(body, "uid", 1000);
    putString(body, "pkg", "com.example");
    CHECK(validate(makeBundle(body.buf())).ok());
}

void testValidNested() {
    Builder inner;
    putString(inner, "name", "payload");
    putInt(inner, "flags", 7);
    const std::vector<std::uint8_t> innerBundle = makeBundle(inner.buf());

    Builder mid;
    putNested(mid, "level2", innerBundle);
    const std::vector<std::uint8_t> midBundle = makeBundle(mid.buf());

    Builder outer;
    putInt(outer, "id", 1);
    putNested(outer, "level1", midBundle);
    putString(outer, "tail", "end");

    netsec::BundleValidationResult result = validate(makeBundle(outer.buf()));
    CHECK(result.ok());
}

void testLengthMismatch() {
    // 声明总长大于实际输入
    {
        Builder body;
        putInt(body, "a", 1);
        std::vector<std::uint8_t> blob = makeBundle(body.buf());
        blob[0] += 4;  // total_len 虚增 4
        CHECK(validate(blob).error == netsec::BundleError::kTruncated);
    }
    // 声明总长小于输入（尾部残余）
    {
        Builder body;
        putInt(body, "a", 1);
        std::vector<std::uint8_t> blob = makeBundle(body.buf());
        blob.push_back(0);
        CHECK(validate(blob).error == netsec::BundleError::kTrailingBytes);
    }
    // 声明总长小于最小头部
    {
        Builder b;
        b.u32(4).bytes("BNDL", 4);
        CHECK(validate(b.buf()).error == netsec::BundleError::kLengthMismatch);
    }
    // 字符串声明长度超过剩余字节
    {
        Builder body;
        putKey(body, "s");
        body.u8(2).u32(100).bytes("ab", 2);
        CHECK(validate(makeBundle(body.buf())).error == netsec::BundleError::kTruncated);
    }
    // 嵌套：外层声明长度超过父 bundle 剩余字节
    {
        Builder inner;
        putInt(inner, "x", 1);
        const std::vector<std::uint8_t> innerBundle = makeBundle(inner.buf());
        Builder body;
        putKey(body, "n");
        body.u8(3).u32(static_cast<std::uint32_t>(innerBundle.size() + 4));
        body.bytes(innerBundle);
        CHECK(validate(makeBundle(body.buf())).error == netsec::BundleError::kTruncated);
    }
    // 嵌套：内部声明总长与外层声明字节数不符
    {
        Builder inner;
        putInt(inner, "x", 1);
        std::vector<std::uint8_t> innerBundle = makeBundle(inner.buf());
        innerBundle[0] += 1;  // 篡改嵌套 bundle 自身的 total_len
        Builder body;
        putNested(body, "n", innerBundle);
        CHECK(validate(makeBundle(body.buf())).error == netsec::BundleError::kLengthMismatch);
    }
    // 嵌套：声明长度小于最小头部
    {
        Builder body;
        putKey(body, "n");
        body.u8(3).u32(4).u32(4);
        CHECK(validate(makeBundle(body.buf())).error == netsec::BundleError::kLengthMismatch);
    }
}

void testDuplicateKey() {
    // 同一作用域内键重复，拒绝
    {
        Builder body;
        putInt(body, "k", 1);
        putString(body, "k", "again");
        netsec::BundleValidationResult result = validate(makeBundle(body.buf()));
        CHECK(result.error == netsec::BundleError::kDuplicateKey);
    }
    // 不同嵌套层级允许同名键
    {
        Builder inner;
        putInt(inner, "k", 2);
        Builder body;
        putInt(body, "k", 1);
        putNested(body, "n", makeBundle(inner.buf()));
        CHECK(validate(makeBundle(body.buf())).ok());
    }
}

void testBudgets() {
    // 总字节预算边界：等于预算通过，超出拒绝
    {
        Builder body;
        putString(body, "s", "0123456789");
        const std::vector<std::uint8_t> blob = makeBundle(body.buf());
        netsec::BundleValidationOptions options;
        options.maxTotalBytes = blob.size();
        CHECK(validate(blob, options).ok());
        options.maxTotalBytes = blob.size() - 1;
        CHECK(validate(blob, options).error == netsec::BundleError::kByteBudgetExceeded);
    }
    // 条目预算边界：恰好等于预算通过，超出拒绝（跨嵌套累计）
    {
        Builder inner;
        putInt(inner, "a", 1);
        putInt(inner, "b", 2);
        Builder body;
        putInt(body, "c", 3);
        putNested(body, "n", makeBundle(inner.buf()));
        const std::vector<std::uint8_t> blob = makeBundle(body.buf());  // 共 4 个条目
        netsec::BundleValidationOptions options;
        options.maxEntries = 4;
        CHECK(validate(blob, options).ok());
        options.maxEntries = 3;
        CHECK(validate(blob, options).error == netsec::BundleError::kEntryBudgetExceeded);
    }
    // 嵌套深度预算
    {
        Builder inner;
        putInt(inner, "x", 1);
        Builder body;
        putNested(body, "n", makeBundle(inner.buf()));
        const std::vector<std::uint8_t> blob = makeBundle(body.buf());
        netsec::BundleValidationOptions options;
        options.maxDepth = 2;
        CHECK(validate(blob, options).ok());
        options.maxDepth = 1;
        CHECK(validate(blob, options).error == netsec::BundleError::kDepthExceeded);
    }
}

void testMalformed() {
    // 空输入
    CHECK(validate({}).error == netsec::BundleError::kTruncated);
    CHECK(netsec::validateBundle(nullptr, 0).error == netsec::BundleError::kTruncated);
    // 魔数错误
    {
        Builder b;
        b.u32(8).bytes("XXXX", 4);
        CHECK(validate(b.buf()).error == netsec::BundleError::kBadMagic);
    }
    // 未知类型码
    {
        Builder body;
        putKey(body, "k");
        body.u8(9).u32(0);
        CHECK(validate(makeBundle(body.buf())).error == netsec::BundleError::kUnknownValueType);
    }
    // 键长超过剩余字节
    {
        Builder body;
        body.u32(50).bytes("ab", 2);
        CHECK(validate(makeBundle(body.buf())).error == netsec::BundleError::kTruncated);
    }
    // 空 bundle（无条目）合法
    CHECK(validate(makeBundle({})).ok());
}

}  // namespace

int main() {
    testValidFlat();
    testValidNested();
    testLengthMismatch();
    testDuplicateKey();
    testBudgets();
    testMalformed();

    if (failures != 0) {
        std::cerr << failures << " check(s) failed" << std::endl;
        return 1;
    }
    std::cout << "all tests passed" << std::endl;
    return 0;
}
