// Package netsec implements tenant-safe NFSv4 file handles for an object
// gateway.  Handles are AEAD-sealed capabilities committed to an export UUID,
// export and object generations, object type, and tenant-policy digest.  The
// AAD binds the handle format version and the server instance.
//
// Parsing a handle yields an opaque Capability.  Every LOOKUP/OPEN/READ/WRITE
// decision must go through Manager, which checks the live export, the exact
// object incarnation, the current tenant policy, and the handle key epoch.
// Reused fsid/inode pairs and backend stable IDs therefore cannot rebind an
// old handle to a new object.
package netsec
