// Package netsec provides admission checks for ACPI tables supplied to the
// kernel during early boot. It validates table checksums and lengths, confines
// table storage to ACPI reclaim or NVS memory, checks XSDT/RSDT pointers, and
// enforces the integrity lock against initrd overrides.
package netsec
