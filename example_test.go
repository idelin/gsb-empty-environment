package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0176"
)

const exampleMetadata = `<?xml version="1.0" encoding="UTF-8"?>
<verification-metadata>
    <configuration>
        <also-trust>
            <sha1/>
        </also-trust>
        <trusted-artifacts>
            <trust group="com.example" name="safe-lib" version="1.0"/>
            <trust group=".*" regex="true"/>
        </trusted-artifacts>
    </configuration>
    <components>
        <component group="com.example" name="safe-lib" version="1.0">
            <artifact name="safe-lib-1.0.jar">
                <sha256 value="deadbeef"/>
            </artifact>
        </component>
    </components>
</verification-metadata>`

const exampleLockfile = `com.example:safe-lib:1.0=runtimeClasspath
`

func ExampleParseMetadata() {
	meta, err := netsec.ParseMetadataBytes([]byte(exampleMetadata))
	if err != nil {
		panic(err)
	}
	lock, err := netsec.ParseLockfileBytes([]byte(exampleLockfile))
	if err != nil {
		panic(err)
	}

	for _, finding := range meta.Analyze(lock) {
		fmt.Printf("%s: %s\n", finding.Severity, finding.ID)
	}

	// A matching primary checksum is accepted directly.
	ok := meta.Verify(netsec.ArtifactRef{
		Group: "com.example", Name: "safe-lib", Version: "1.0", File: "safe-lib-1.0.jar",
	}, map[string]string{"sha256": "deadbeef"})
	fmt.Println("verified artifact allowed:", ok.Allowed)

	// Output:
	// risk: overbroad-regex
	// risk: unescaped-dot
	// verified artifact allowed: true
}
