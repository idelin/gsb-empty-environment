// Package netsec audits AndroidManifest.xml files for exported components
// that are not protected by a signature-level permission.
//
// Export resolution follows the platform defaults:
//   - An explicit android:exported always wins.
//   - Below targetSdk 31 a component without android:exported is exported when
//     it declares an intent-filter.
//   - On targetSdk 31+ an intent-filter component missing android:exported is
//     an error, matching the Android 12 install-time requirement.
//   - A <provider> defaults to exported up to targetSdk 16 and not exported
//     from targetSdk 17.
//   - An <activity-alias> follows its targetActivity unless it sets
//     android:exported itself.
//
// Protection resolution uses the component-level android:permission, falling
// back to the application-level one. A guard is weak when the permission is
// undefined in the manifest, has no protectionLevel, or uses normal or
// dangerous protection; signature-family protection is considered safe.
package netsec
