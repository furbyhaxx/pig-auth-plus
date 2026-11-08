package claude

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PinnedVersion is the Claude Code release reported when no newer trustworthy one is known.
// Anthropic gates newer models on it (400 `claude_code_version_too_old`).
const PinnedVersion = "2.1.306"

// VersionEnv overrides the reported version.
const VersionEnv = "ANTHROPIC_CLAUDE_CODE_VERSION"

var (
	versionRE       = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	versionInOutput = regexp.MustCompile(`(\d+\.\d+\.\d+)`)
)

func userAgent(version string) string { return "claude-cli/" + version + " (external, cli)" }

// isOlder compares numerically: 2.1.99 is older than 2.1.306.
func isOlder(candidate, baseline string) bool {
	a, b := strings.Split(candidate, "."), strings.Split(baseline, ".")
	for i := range 3 {
		var x, y uint64
		if i < len(a) {
			x, _ = strconv.ParseUint(a[i], 10, 64)
		}
		if i < len(b) {
			y, _ = strconv.ParseUint(b[i], 10, 64)
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// version resolves the reported version lazily, once: env override > installed `claude`
// when not older than the pin > pin. Warning is set for an outdated or malformed override.
type version struct {
	env   func(string) string
	probe func() string

	once    sync.Once
	value   string
	warning string
}

func newVersion(env func(string) string) *version {
	return &version{env: env, probe: probeInstalledClaude}
}

func (v *version) get() string {
	v.once.Do(v.resolve)
	return v.value
}

func (v *version) resolve() {
	raw, set := v.env(VersionEnv), v.env(VersionEnv) != ""
	if set {
		ver := strings.TrimSpace(raw)
		switch {
		case !versionRE.MatchString(ver):
			v.value = PinnedVersion
			v.warning = fmt.Sprintf("claude: %s=%q is not a major.minor.patch version; reporting %s instead", VersionEnv, raw, PinnedVersion)
		case isOlder(ver, PinnedVersion):
			v.value = ver
			v.warning = fmt.Sprintf("claude: %s=%s is older than the pinned %s; newer models may refuse it", VersionEnv, ver, PinnedVersion)
		default:
			v.value = ver
		}
		return
	}
	v.value = pickDetected(v.probe())
}

func pickDetected(output string) string {
	m := versionInOutput.FindStringSubmatch(output)
	if m == nil || isOlder(m[1], PinnedVersion) {
		return PinnedVersion
	}
	return m[1]
}

func probeInstalledClaude() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "--version").Output()
	if err != nil {
		return ""
	}
	return string(out)
}
