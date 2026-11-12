package netsec

import (
	"encoding/binary"
	"errors"
	"testing"
)

func newPerfAttr(size uint32) []byte {
	raw := make([]byte, int(size))
	if len(raw) < 8 {
		return raw
	}
	binary.LittleEndian.PutUint32(raw[0:4], PerfTypeHardware)
	binary.LittleEndian.PutUint32(raw[4:8], size)
	return raw
}

func putUint64(raw []byte, offset int, value uint64) {
	binary.LittleEndian.PutUint64(raw[offset:offset+8], value)
}

func validPerfAttr() []byte {
	raw := newPerfAttr(PerfEventAttrSize)
	putUint64(raw, 8, 1) // PERF_COUNT_HW_INSTRUCTIONS
	putUint64(raw, 40, PerfFlagExcludeKernel|PerfFlagExcludeHV)
	return raw
}

func TestValidatePerfEventAttrAllowsUserCounter(t *testing.T) {
	for _, pidCPU := range []struct {
		pid int
		cpu int
	}{
		{pid: 0, cpu: -1},
		{pid: 0, cpu: 0},
	} {
		if err := ValidatePerfEventAttr(validPerfAttr(), pidCPU.pid, pidCPU.cpu); err != nil {
			t.Fatalf("ValidatePerfEventAttr(pid=%d, cpu=%d): %v", pidCPU.pid, pidCPU.cpu, err)
		}
	}
}

func TestValidatePerfEventAttrAllowsKnownSamplingFlags(t *testing.T) {
	raw := validPerfAttr()
	putUint64(raw, 24, 1<<0)                     // PERF_SAMPLE_IP is not an address sample.
	putUint64(raw, 32, 1<<0|1<<1|1<<2|1<<3|1<<4) // all defined read formats.
	if err := ValidatePerfEventAttr(raw, 0, -1); err != nil {
		t.Fatalf("legitimate sampling flags rejected: %v", err)
	}
}

func TestParsePerfEventAttrLayout(t *testing.T) {
	raw := newPerfAttr(PerfEventAttrSize)
	binary.LittleEndian.PutUint32(raw[0:4], PerfTypeSoftware)
	putUint64(raw, 8, 0x11)
	putUint64(raw, 16, 0x22)
	putUint64(raw, 24, 0x33)
	putUint64(raw, 32, 0x44)
	putUint64(raw, 40, 0x55)
	binary.LittleEndian.PutUint32(raw[48:52], 0x66)
	binary.LittleEndian.PutUint32(raw[52:56], 0x77)
	putUint64(raw, 56, 0x88)
	putUint64(raw, 64, 0x99)
	putUint64(raw, 72, 0xaa)
	putUint64(raw, 80, 0xbb)
	binary.LittleEndian.PutUint32(raw[88:92], 0xcc)
	binary.LittleEndian.PutUint32(raw[92:96], uint32(0xdd))
	putUint64(raw, 96, 0xee)
	binary.LittleEndian.PutUint32(raw[104:108], 0xff)
	binary.LittleEndian.PutUint16(raw[108:110], 0x12)
	binary.LittleEndian.PutUint32(raw[112:116], 0x13)
	putUint64(raw, 120, 0x14)
	putUint64(raw, 128, 0x15)

	attr, err := ParsePerfEventAttr(raw)
	if err != nil {
		t.Fatalf("ParsePerfEventAttr: %v", err)
	}
	if attr.Type != PerfTypeSoftware || attr.Size != PerfEventAttrSize {
		t.Fatalf("header = type %d size %d", attr.Type, attr.Size)
	}
	if attr.Config != 0x11 || attr.SamplePeriod != 0x22 || attr.SampleType != 0x33 ||
		attr.ReadFormat != 0x44 || attr.Flags != 0x55 || attr.BranchSampleType != 0xaa {
		t.Fatalf("decoded policy fields do not match byte layout: %+v", attr)
	}
}

func TestValidatePerfEventAttrRejectsWrongPID(t *testing.T) {
	for _, pid := range []int{-1, 1, 1234} {
		err := ValidatePerfEventAttr(validPerfAttr(), pid, -1)
		if !errors.Is(err, ErrNotCurrentThread) {
			t.Fatalf("pid=%d: got %v, want ErrNotCurrentThread", pid, err)
		}
	}
}

func TestValidatePerfEventAttrRejectsInvalidCPU(t *testing.T) {
	if err := ValidatePerfEventAttr(validPerfAttr(), 0, -2); !errors.Is(err, ErrInvalidCPU) {
		t.Fatalf("got %v, want ErrInvalidCPU", err)
	}
}

func TestValidatePerfEventAttrRejectsRawEvent(t *testing.T) {
	raw := validPerfAttr()
	binary.LittleEndian.PutUint32(raw[0:4], PerfTypeRaw)
	if err := ValidatePerfEventAttr(raw, 0, -1); !errors.Is(err, ErrRawEvent) {
		t.Fatalf("got %v, want ErrRawEvent", err)
	}
}

func TestValidatePerfEventAttrRejectsAddressSampling(t *testing.T) {
	tests := []struct {
		name  string
		mask  uint64
		error error
	}{
		{name: "ADDR", mask: PerfSampleAddr, error: ErrAddressSampling},
		{name: "PHYS_ADDR", mask: PerfSamplePhysAddr, error: ErrPhysAddressSampling},
		{name: "LBR sample", mask: PerfSampleBranchStack, error: ErrLBRSampling},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := validPerfAttr()
			putUint64(raw, 24, 1<<0|tc.mask)
			if err := ValidatePerfEventAttr(raw, 0, -1); !errors.Is(err, tc.error) {
				t.Fatalf("got %v, want %v", err, tc.error)
			}
		})
	}

	raw := validPerfAttr()
	putUint64(raw, 72, 1) // branch_sample_type selects LBR even without the sample bit.
	if err := ValidatePerfEventAttr(raw, 0, -1); !errors.Is(err, ErrLBRSampling) {
		t.Fatalf("got %v, want ErrLBRSampling", err)
	}
}

func TestValidatePerfEventAttrRejectsInheritance(t *testing.T) {
	for _, flag := range []uint64{
		PerfFlagInherit,
		PerfFlagInheritStat,
		PerfFlagInheritThread,
	} {
		raw := validPerfAttr()
		putUint64(raw, 40, PerfFlagExcludeKernel|PerfFlagExcludeHV|flag)
		if err := ValidatePerfEventAttr(raw, 0, -1); !errors.Is(err, ErrInherit) {
			t.Fatalf("flag %#x: got %v, want ErrInherit", flag, err)
		}
	}
}

func TestValidatePerfEventAttrRequiresKernelAndHypervisorExclusion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags uint64
		error error
	}{
		{
			name:  "missing kernel exclusion",
			flags: PerfFlagExcludeHV,
			error: ErrKernelScope,
		},
		{
			name:  "missing hypervisor exclusion",
			flags: PerfFlagExcludeKernel,
			error: ErrHypervisorScope,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := validPerfAttr()
			putUint64(raw, 40, tc.flags)
			if err := ValidatePerfEventAttr(raw, 0, -1); !errors.Is(err, tc.error) {
				t.Fatalf("got %v, want %v", err, tc.error)
			}
		})
	}
}

func TestValidatePerfEventAttrRejectsLegacySize(t *testing.T) {
	for _, size := range []uint32{64, 72} {
		raw := newPerfAttr(size)
		putUint64(raw, 40, PerfFlagExcludeKernel|PerfFlagExcludeHV)
		if err := ValidatePerfEventAttr(raw, 0, -1); !errors.Is(err, ErrInvalidAttrSize) {
			t.Fatalf("size=%d: got %v, want ErrInvalidAttrSize", size, err)
		}
	}

	if err := ValidatePerfEventAttr(make([]byte, 4), 0, -1); !errors.Is(err, ErrInvalidPerfEventAttr) {
		t.Fatalf("short legacy attr: got %v, want ErrInvalidPerfEventAttr", err)
	}
}

func TestValidatePerfEventAttrAcceptsPublishedLegacyLayout(t *testing.T) {
	for _, size := range []uint32{80, 96, 104, 112, 120, 128} {
		raw := newPerfAttr(size)
		putUint64(raw, 8, 1)
		putUint64(raw, 40, PerfFlagExcludeKernel|PerfFlagExcludeHV)
		if err := ValidatePerfEventAttr(raw, 0, -1); err != nil {
			t.Fatalf("size=%d layout rejected: %v", size, err)
		}
	}
}

func TestValidatePerfEventAttrRejectsSizeMismatch(t *testing.T) {
	raw := validPerfAttr()
	binary.LittleEndian.PutUint32(raw[4:8], 120)
	if err := ValidatePerfEventAttr(raw, 0, -1); !errors.Is(err, ErrInvalidAttrSize) {
		t.Fatalf("declared size does not match bytes: got %v", err)
	}

	raw = validPerfAttr()
	binary.LittleEndian.PutUint32(raw[4:8], PerfEventAttrSize+8)
	if err := ValidatePerfEventAttr(raw, 0, -1); !errors.Is(err, ErrInvalidAttrSize) {
		t.Fatalf("oversized attr: got %v", err)
	}

	raw = validPerfAttr()
	binary.LittleEndian.PutUint32(raw[4:8], 100)
	raw = raw[:100]
	if err := ValidatePerfEventAttr(raw, 0, -1); !errors.Is(err, ErrInvalidAttrSize) {
		t.Fatalf("unpublished size: got %v", err)
	}
}

func TestValidatePerfEventAttrRejectsUnknownBits(t *testing.T) {
	for _, offset := range []int{24, 32, 40} {
		raw := validPerfAttr()
		putUint64(raw, offset, 1<<63)
		if err := ValidatePerfEventAttr(raw, 0, -1); !errors.Is(err, ErrUnknownPerfFlag) {
			t.Fatalf("offset=%d: got %v, want ErrUnknownPerfFlag", offset, err)
		}
	}

	raw := validPerfAttr()
	putUint64(raw, 40, PerfFlagExcludeKernel|PerfFlagExcludeHV|1<<38)
	if err := ValidatePerfEventAttr(raw, 0, -1); !errors.Is(err, ErrUnknownPerfFlag) {
		t.Fatalf("reserved flag bit: got %v, want ErrUnknownPerfFlag", err)
	}
}

func TestValidatePerfEventAttrRejectsTruncatedInput(t *testing.T) {
	for _, n := range []int{0, 1, 4, 7} {
		if err := ValidatePerfEventAttr(make([]byte, n), 0, -1); !errors.Is(err, ErrInvalidPerfEventAttr) {
			t.Fatalf("length=%d: got %v, want ErrInvalidPerfEventAttr", n, err)
		}
	}
}

func FuzzValidatePerfEventAttrDoesNotPanic(f *testing.F) {
	f.Add(validPerfAttr(), 0, -1)
	f.Add(newPerfAttr(64), -1, 0)
	f.Add([]byte{0, 0, 0, 0}, 0, 0)
	f.Fuzz(func(t *testing.T, raw []byte, pid int, cpu int) {
		_ = ValidatePerfEventAttr(raw, pid, cpu)
	})
}
