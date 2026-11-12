package netsec

// Platform reserved IOVA windows. These ranges are intercepted by the
// host interrupt controller and chipset emulation; allowing a passthrough
// device to DMA into them would let a malicious guest forge interrupts
// (MSI window) or corrupt emulated chipset state.
const (
	// MSIWindowBase is the first address of the x86 APIC MSI interrupt
	// window; writes to 0xFEE00000-0xFEEFFFFF are interpreted as
	// interrupt messages, not memory transactions.
	MSIWindowBase uint64 = 0xFEE00000
	// MSIWindowEnd is the exclusive end of the MSI interrupt window.
	MSIWindowEnd uint64 = 0xFEF00000

	ioAPICBase uint64 = 0xFEC00000
	ioAPICEnd  uint64 = 0xFEC01000

	hpetBase uint64 = 0xFED00000
	hpetEnd  uint64 = 0xFED00400
)

// DefaultReservedRanges returns a fresh copy of the platform reserved
// IOVA windows applied to every new domain. The MSI interrupt window
// 0xFEE00000-0xFEEFFFFF is always included.
func DefaultReservedRanges() []Range {
	return []Range{
		{Base: ioAPICBase, End: ioAPICEnd},
		{Base: hpetBase, End: hpetEnd},
		{Base: MSIWindowBase, End: MSIWindowEnd},
	}
}
