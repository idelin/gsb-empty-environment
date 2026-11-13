package netsec

import (
	"fmt"
	"io/fs"
	"reflect"
	"time"
)

// FileIdentity is the stable identity and change-detection metadata of a
// frozen file. Device, inode, and generation identify the file; the remaining
// fields make replacement, truncation, appends, and in-place writes visible.
type FileIdentity struct {
	Device     uint64 `json:"device"`
	Inode      uint64 `json:"inode"`
	Generation uint64 `json:"generation"`
	Links      uint64 `json:"links"`
	Size       int64  `json:"size"`
	ModTime    int64  `json:"mtime_ns"`
	ChangeTime int64  `json:"ctime_ns"`
}

// Entry describes a directory entry as observed through a directory handle.
type Entry struct {
	Mode     fs.FileMode
	Size     int64
	Identity FileIdentity
}

func (e Entry) regular() bool {
	return e.Mode.IsRegular()
}

func (e Entry) dir() bool {
	return e.Mode.IsDir()
}

func (e Entry) symlink() bool {
	return e.Mode&fs.ModeSymlink != 0
}

func entryFromFileInfo(info fs.FileInfo) (Entry, error) {
	if info == nil {
		return Entry{}, fmt.Errorf("netsec: nil file info")
	}
	identity := FileIdentity{
		Size:    info.Size(),
		ModTime: info.ModTime().UnixNano(),
	}
	if sys := info.Sys(); sys != nil {
		value := reflect.ValueOf(sys)
		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return Entry{Mode: info.Mode(), Size: info.Size(), Identity: identity}, nil
			}
			value = value.Elem()
		}
		if value.Kind() == reflect.Struct {
			identity.Device, _ = reflectUintField(value, "Dev")
			identity.Inode, _ = reflectUintField(value, "Ino")
			identity.Generation, _ = reflectUintField(value, "Gen")
			identity.Links, _ = reflectUintField(value, "Nlink")
			if t, ok := reflectTimeField(value, "Mtim", "Mtimespec", "Mtime"); ok {
				identity.ModTime = t
			}
			if t, ok := reflectTimeField(value, "Ctim", "Ctimespec", "Ctime"); ok {
				identity.ChangeTime = t
			}
		}
	}
	return Entry{
		Mode:     info.Mode(),
		Size:     info.Size(),
		Identity: identity,
	}, nil
}

func reflectUintField(value reflect.Value, name string) (uint64, bool) {
	field := value.FieldByName(name)
	if !field.IsValid() {
		return 0, false
	}
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return field.Uint(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint64(field.Int()), true
	default:
		return 0, false
	}
}

func reflectTimeField(value reflect.Value, names ...string) (int64, bool) {
	for _, name := range names {
		field := value.FieldByName(name)
		if !field.IsValid() {
			continue
		}
		switch field.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return field.Int(), true
		case reflect.Struct:
			sec, ok := reflectUintField(field, "Sec")
			if !ok {
				continue
			}
			nsec, _ := reflectUintField(field, "Nsec")
			return int64(sec)*int64(time.Second) + int64(nsec), true
		}
	}
	return 0, false
}

func sameIdentity(a, b FileIdentity) bool {
	return a == b
}

// sameFrozenIdentity compares the identity fields that must not change for a
// frozen handle. A changed link count means the pathname was unlinked,
// renamed, or hardlinked; in that case ctime is intentionally ignored because
// the already-open file is still the same inode. If the link count is stable,
// a ctime change indicates an in-place write or metadata change and
// invalidates the instance.
func sameFrozenIdentity(a, b FileIdentity) bool {
	if a.Device != b.Device ||
		a.Inode != b.Inode ||
		a.Generation != b.Generation ||
		a.Size != b.Size ||
		a.ModTime != b.ModTime {
		return false
	}
	if a.Links == b.Links && a.ChangeTime != b.ChangeTime {
		return false
	}
	return true
}

func identityKey(identity FileIdentity) string {
	return fmt.Sprintf("%d:%d:%d", identity.Device, identity.Inode, identity.Generation)
}
