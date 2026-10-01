// Package wire 实现一个"稳定字段编号"的二进制序列化格式。
//
// 支持的序列化范围（显式声明，除此之外一律拒绝）：
//   - 标量：uint64、int64（zigzag）、bool
//   - 变长：string、[]byte
//   - 不支持：浮点、嵌套消息、map、指针、接口、反射构造的任意类型
//
// 安全边界：
//   - 解码只构造上述普通 Go 值，绝不执行宿主代码（不用 reflect 构造类型、
//     不用 gob/plugin/exec，数据流中不存在任何可触发的行为）。
//   - 先验证长度再分配：任何 []byte/string 在分配前必须满足
//     声明长度 <= 剩余输入 且 <= MaxFieldLen。
//   - 字段编号稳定：删除的编号通过 Schema.Reserve 永久保留，禁止复用为
//     不相关类型；同一编号重复定义或线上类型冲突一律拒绝。
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// 帧头：magic "SW" + 格式版本 0x01 + uvarint(数据版本)。
var magic = []byte{'S', 'W', 0x01}

// MaxFieldLen 是单个变长字段允许的最大字节数（64 MiB）。
// 超过该上限的声明长度在分配前即被拒绝。
const MaxFieldLen = 1 << 26

// 线类型（wire type），编码进 tag 低 3 位。
const (
	wtVarint = 0 // uint64 / int64(zigzag) / bool
	wtBytes  = 2 // string / []byte，uvarint 长度前缀
)

// FieldType 是字段的逻辑类型。
type FieldType int

const (
	TUint64 FieldType = iota
	TInt64
	TBool
	TString
	TBytes
)

func (t FieldType) String() string {
	switch t {
	case TUint64:
		return "uint64"
	case TInt64:
		return "int64"
	case TBool:
		return "bool"
	case TString:
		return "string"
	case TBytes:
		return "bytes"
	}
	return fmt.Sprintf("FieldType(%d)", int(t))
}

func (t FieldType) wireType() int {
	if t == TString || t == TBytes {
		return wtBytes
	}
	return wtVarint
}

// MissingRequiredError 报告缺失的必填字段编号。
type MissingRequiredError struct {
	Nums []int
}

func (e *MissingRequiredError) Error() string {
	return fmt.Sprintf("missing required field number(s): %v", e.Nums)
}

// IsMissingRequired 判断 err 是否为缺必填字段错误，并返回具体编号。
func IsMissingRequired(err error) ([]int, bool) {
	var mre *MissingRequiredError
	if errors.As(err, &mre) {
		return mre.Nums, true
	}
	return nil, false
}

func appendTag(buf []byte, num, wt int) []byte {
	return binary.AppendUvarint(buf, uint64(num)<<3|uint64(wt))
}

func zigzagEncode(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }
func zigzagDecode(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }
