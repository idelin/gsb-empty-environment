package pager

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

// 游标与分页相关的错误。调用方可用 == 判定。
var (
	// ErrCursorMalformed 游标无法解码或结构非法。
	ErrCursorMalformed = errors.New("pager: malformed cursor")
	// ErrSnapshotExpired 游标绑定的快照不存在或已释放。
	ErrSnapshotExpired = errors.New("pager: snapshot expired or unknown")
	// ErrSortMismatch 游标绑定的排序规则与本次请求不一致。
	ErrSortMismatch = errors.New("pager: sort spec does not match cursor")
	// ErrDirectionMismatch 游标方向与本次请求方向不一致
	// （NextCursor 只能正向续查，PrevCursor 只能反向续查）。
	ErrDirectionMismatch = errors.New("pager: direction does not match cursor")
	// ErrInvalidPageSize 页大小非法。
	ErrInvalidPageSize = errors.New("pager: page size must be positive")
	// ErrNoSortFields 未提供排序字段。
	ErrNoSortFields = errors.New("pager: at least one sort field is required")
)

// Direction 翻页方向。
type Direction int

const (
	// Forward 正向（朝排序末尾方向）翻页。
	Forward Direction = iota
	// Backward 反向（朝排序开头方向）翻页。
	Backward
)

const cursorPrefix = "sp1:"

// cursor 绑定快照、排序规则指纹、方向与键集位置（排序键 + 唯一编号）。
type cursor struct {
	V        int       `json:"v"`
	Snapshot string    `json:"s"`
	SortHash string    `json:"h"`
	Dir      Direction `json:"d"`
	Keys     []Value   `json:"k"`
	ID       string    `json:"id"`
}

func encodeCursor(c cursor) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return cursorPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeCursor(s string) (cursor, error) {
	var c cursor
	if len(s) <= len(cursorPrefix) || s[:len(cursorPrefix)] != cursorPrefix {
		return c, ErrCursorMalformed
	}
	raw, err := base64.RawURLEncoding.DecodeString(s[len(cursorPrefix):])
	if err != nil {
		return c, ErrCursorMalformed
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, ErrCursorMalformed
	}
	if c.V != 1 || c.Snapshot == "" || c.SortHash == "" {
		return c, ErrCursorMalformed
	}
	return c, nil
}
