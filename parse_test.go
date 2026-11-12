package netsec_test

import (
	"errors"
	"strings"
	"testing"

	netsec "example.org/netsec/q0215"
)

const sampleManifest = `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.example.app">
    <uses-sdk android:targetSdkVersion="30" />
    <permission android:name="com.example.SIG" android:protectionLevel="signature" />
    <permission android:name="com.example.NOLEVEL" />
    <application android:permission="com.example.NOLEVEL">
        <activity android:name=".ExposedActivity">
            <intent-filter>
                <action android:name="android.intent.action.VIEW" />
            </intent-filter>
        </activity>
        <activity android:name=".GuardedActivity"
            android:permission="com.example.SIG" android:exported="true" />
        <activity-alias android:name=".Alias"
            android:targetActivity=".ExposedActivity">
            <intent-filter>
                <action android:name="android.intent.action.MAIN" />
            </intent-filter>
        </activity-alias>
        <service android:name=".ExportedService" android:exported="true"
            android:permission="com.example.UNDEFINED" />
        <provider android:name=".PrivateProvider" android:authorities="com.example.p" />
    </application>
</manifest>`

func TestParseManifestAndAnalyze(t *testing.T) {
	m, err := netsec.ParseManifest(strings.NewReader(sampleManifest))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if m.Package != "com.example.app" || m.TargetSDK != 30 {
		t.Fatalf("unexpected header: package=%q targetSdk=%d", m.Package, m.TargetSDK)
	}
	if len(m.Application.Activities) != 2 || len(m.Application.ActivityAliases) != 1 ||
		len(m.Application.Services) != 1 || len(m.Application.Providers) != 1 {
		t.Fatalf("unexpected component counts: %+v", m.Application)
	}

	activity := m.Application.Activities[0]
	if !activity.HasFilter || activity.Exported != nil {
		t.Fatalf("intent-filter and exported state parsed incorrectly: %+v", activity)
	}
	provider := m.Application.Providers[0]
	if provider.HasFilter || provider.Exported != nil {
		t.Fatalf("provider parsed incorrectly: %+v", provider)
	}

	report, err := netsec.Analyze(m)
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}

	byName := map[string]netsec.Finding{}
	for _, f := range report.Findings {
		byName[f.Name] = f
	}

	exposed, ok := byName[".ExposedActivity"]
	if !ok || exposed.Reason != netsec.ReasonProtectionMissing {
		t.Fatalf("expected nolevel-protected activity finding, got %+v", byName)
	}
	alias, ok := byName[".Alias"]
	if !ok || alias.Kind != netsec.KindActivityAlias {
		t.Fatalf("expected alias finding following exposed target, got %+v", byName)
	}
	service, ok := byName[".ExportedService"]
	if !ok || service.Reason != netsec.ReasonPermissionUndefined {
		t.Fatalf("expected undefined-permission service finding, got %+v", byName)
	}
	if _, ok := byName[".GuardedActivity"]; ok {
		t.Fatal("signature-guarded activity must not be flagged")
	}
	if _, ok := byName[".PrivateProvider"]; ok {
		t.Fatal("provider defaulting to private on sdk 30 must not be flagged")
	}
}

func TestParseManifestRejectsInvalidXML(t *testing.T) {
	if _, err := netsec.ParseManifest(strings.NewReader("<manifest")); err == nil {
		t.Fatal("expected parse error for truncated XML")
	}
}

func TestAnalyzeNilManifest(t *testing.T) {
	report, err := netsec.Analyze(nil)
	if err == nil || report != nil {
		t.Fatalf("expected error and nil report, got report=%v err=%v", report, err)
	}
}

func TestParseManifestModernExportedError(t *testing.T) {
	manifest := `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <uses-sdk android:targetSdkVersion="31" />
    <application>
        <activity android:name=".NoExported">
            <intent-filter><action android:name="x" /></intent-filter>
        </activity>
    </application>
</manifest>`
	m, err := netsec.ParseManifest(strings.NewReader(manifest))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	var attrErr *netsec.ExportedAttributeError
	if _, err := netsec.Analyze(m); !errors.As(err, &attrErr) {
		t.Fatalf("expected ExportedAttributeError on sdk 31, got %v", err)
	}
}
