// Package netsec detects unsafe use of JNI critical array pointers.
//
// The host feeds JNI trace records to a Detector with Record or Feed. The
// detector reports other JNI calls and blocking calls made while a
// GetPrimitiveArrayCritical pointer is held, out-of-bounds pointer accesses,
// reads and writes after ReleasePrimitiveArrayCritical, duplicate releases,
// arrays left unreleased when a thread ends, and writes discarded by a
// JNI_ABORT release.
//
// Example:
//
//	d := netsec.NewDetector()
//	d.Record(netsec.GetEvent("t1", "arr", 0x1000, 8))
//	d.Record(netsec.JNICallEvent("t1", "NewStringUTF")) // violation
//	d.Record(netsec.WriteEvent("t1", 0x1008, 1))       // one byte past end
//	d.Record(netsec.ReleaseEvent("t1", "arr", 0x1000, 8, netsec.ReleaseModeDefault))
//	d.Record(netsec.WriteEvent("t1", 0x1000, 1))       // use after release
package netsec
