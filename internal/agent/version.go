// The claude CLI's identity, for the per-run diagnostics line. The field
// dead-end took days to diagnose partly because no log recorded WHICH claude
// answered - permission behavior shifts between CLI releases, and pie's logs
// showed only pie's own version.
package agent

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	cliVersionOnce sync.Once
	cliVersion     string
)

// CLIVersion returns `claude --version` output, cached for the process
// lifetime (runs spawn many agents; the CLI does not change mid-run).
// Failure degrades to a labelled unknown rather than an error - this is
// diagnostics, never a gate.
func CLIVersion() string {
	cliVersionOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "claude", "--version").Output()
		if err != nil {
			cliVersion = "unknown (" + err.Error() + ")"
			return
		}
		cliVersion = strings.TrimSpace(string(out))
	})
	return cliVersion
}

// SplitRules splits an allowlist string into whole rules the way the
// permission system reads them: a Bash(...) rule is one token even when its
// head has spaces, which a bare strings.Fields would cut in half.
func SplitRules(allowed string) []string {
	return allowRuleToken.FindAllString(allowed, -1)
}

// CountRules counts whole allowlist rules (see SplitRules).
func CountRules(allowed string) int {
	return len(SplitRules(allowed))
}
