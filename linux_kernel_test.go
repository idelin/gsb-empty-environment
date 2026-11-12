//go:build linux && (amd64 || arm64)

package netsec

import (
	"syscall"
	"testing"
	"unsafe"
)

func ioc(dir, typ, nr, size uintptr) uintptr {
	return dir<<30 | typ<<8 | nr | size<<16
}

func TestLinuxIOCTLEncodingsMatchUAPI(t *testing.T) {
	const (
		magic = uintptr('!')
		write = 1
		read  = 2
	)
	if got, want := ioc(write, magic, 2, unsafe.Sizeof(uint64(0))), ioctlNotifIDValid; got != want {
		t.Fatalf("ID_VALID ioctl = %#x, want %#x", got, want)
	}
	if got, want := ioc(read|write, magic, 1, unsafe.Sizeof(seccompNotifResp{})), ioctlNotifSend; got != want {
		t.Fatalf("SEND ioctl = %#x, want %#x", got, want)
	}
	if got, want := ioc(write, magic, 3, unsafe.Sizeof(seccompNotifAddfd{})), ioctlNotifAddFD; got != want {
		t.Fatalf("ADDFD ioctl = %#x, want %#x", got, want)
	}
	if got, want := ioc(read|write, magic, 0, unsafe.Sizeof(seccompNotif{})), ioctlNotifRecv; got != want {
		t.Fatalf("RECV ioctl = %#x, want %#x", got, want)
	}
}

func TestRelativeToRoot(t *testing.T) {
	tests := []struct {
		root, target string
		want         string
		wantErr      error
	}{
		{root: "/", target: "/", want: ""},
		{root: "/", target: "/work", want: "work"},
		{root: "/jail", target: "/jail", want: ""},
		{root: "/jail", target: "/jail/work", want: "work"},
		{root: "/jail", target: "/jailx/work", wantErr: syscall.EXDEV},
		{root: "/jail", target: "/outside", wantErr: syscall.EXDEV},
	}
	for _, tt := range tests {
		got, err := relativeToRoot(tt.root, tt.target)
		if tt.wantErr != nil {
			if err == nil || err != tt.wantErr {
				t.Fatalf("relativeToRoot(%q, %q) err = %v, want %v", tt.root, tt.target, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Fatalf("relativeToRoot(%q, %q) = %q, %v; want %q", tt.root, tt.target, got, err, tt.want)
		}
	}
}
