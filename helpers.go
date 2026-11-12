package netsec

import (
	"fmt"
	"time"
)

// nowUnixNano 返回墙上时间的逻辑时间戳。
func nowUnixNano() Time {
	return Time(time.Now().UnixNano())
}

func wrapID(err error, id string) error {
	return fmt.Errorf("%w: %s", err, id)
}

func containsVersion(list []ExceptionVersion, v ExceptionVersion) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// versionLess 给出 ExceptionVersion 的确定性排序：先 ID 后世代。
func versionLess(a, b ExceptionVersion) bool {
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.Generation < b.Generation
}

func cloneVersions(in []ExceptionVersion) []ExceptionVersion {
	if len(in) == 0 {
		return []ExceptionVersion{}
	}
	out := make([]ExceptionVersion, len(in))
	copy(out, in)
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneException(e *Exception) *Exception {
	if e == nil {
		return nil
	}
	cp := *e
	return &cp
}

func cloneOperation(o *Operation) *Operation {
	if o == nil {
		return nil
	}
	cp := *o
	return &cp
}

func cloneEvent(e *InvalidationEvent) *InvalidationEvent {
	if e == nil {
		return nil
	}
	cp := *e
	return &cp
}
