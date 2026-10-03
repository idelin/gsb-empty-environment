package lsm

import "errors"

// ErrNotFound 在 Key 不存在、或最新版本是 Tombstone 时返回。
var ErrNotFound = errors.New("lsm: key not found")

// ErrClosed 在 DB 关闭后继续使用时返回。
var ErrClosed = errors.New("lsm: database is closed")

// ErrCorrupt 表示 SSTable 文件格式非法。
var ErrCorrupt = errors.New("lsm: corrupt sstable")
