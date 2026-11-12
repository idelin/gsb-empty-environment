package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// TagSet 是一组访问标签。
type TagSet map[string]string

var (
	// ErrLockedTagConflict 表示请求标签试图覆盖不可覆盖标签。
	ErrLockedTagConflict = errors.New("netsec: requested tag conflicts with locked tag")
	// ErrInvalidTag 表示标签键或值非法。
	ErrInvalidTag = errors.New("netsec: invalid tag")
)

// MergeTags 计算最终生效的标签集合：
// 继承标签作为基底，请求标签覆盖继承标签，不可覆盖标签永远胜出。
// 若请求标签试图以不同值覆盖不可覆盖标签，则返回错误。
// 该校验必须在任何可见对象被分配之前完成。
func MergeTags(requested, inherited, locked TagSet) (TagSet, error) {
	for _, set := range []TagSet{requested, inherited, locked} {
		for k, v := range set {
			if err := validateTag(k, v); err != nil {
				return nil, err
			}
		}
	}
	for k, lockedV := range locked {
		if reqV, ok := requested[k]; ok && reqV != lockedV {
			return nil, fmt.Errorf("%w: %q requested=%q locked=%q",
				ErrLockedTagConflict, k, reqV, lockedV)
		}
	}
	merged := make(TagSet, len(inherited)+len(requested)+len(locked))
	for k, v := range inherited {
		merged[k] = v
	}
	for k, v := range requested {
		merged[k] = v
	}
	for k, v := range locked {
		merged[k] = v
	}
	return merged, nil
}

func validateTag(key, value string) error {
	if key == "" || strings.ContainsAny(key, "\x00\n\r") {
		return fmt.Errorf("%w: bad key %q", ErrInvalidTag, key)
	}
	if strings.ContainsAny(value, "\x00\n\r") {
		return fmt.Errorf("%w: bad value for key %q", ErrInvalidTag, key)
	}
	return nil
}

// Digest 返回标签集合的规范化摘要。权限检查绑定该摘要，
// 因此授权决策始终针对最终合并结果而非请求方声称的标签。
func Digest(tags TagSet) string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(tags[k]))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
