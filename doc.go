// Package netsec provides an in-memory IOMMU model for isolated DMA access.
//
// The package models 4 KiB translations without touching real memory or
// hardware. A host control plane assigns physical pages to tenants, registers
// devices, and installs translations. Device code can only submit Transfer
// requests; every page in a request is validated before a memory callback is
// invoked.
package netsec
