package strictjson

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The toolkit builds only where int has 64 bits.
func TestThirtyTwoBitPlatformsDoNotBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the package for 386")
	}

	command := exec.Command("go", "build", "-buildvcs=false", ".")
	command.Env = append(os.Environ(), "GOOS=linux", "GOARCH=386", "CGO_ENABLED=0")
	output, err := command.CombinedOutput()

	if err == nil || !strings.Contains(string(output), "overflows") {
		t.Errorf("building for linux/386: %v\n%s", err, output)
	}
}
