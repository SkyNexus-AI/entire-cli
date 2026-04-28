package cli

import (
	"fmt"
	"sync"

	"github.com/spf13/cobra"
)

// deprecationWarnOnce stores one *sync.Once per (old → replacement) pair so a
// long-running process never emits the same deprecation warning twice.
var deprecationWarnOnce sync.Map // key: "old->replacement", value: *sync.Once

// warnDeprecatedAliasOnce prints a single stderr line per process for the given
// deprecation pair. The oldUse argument is matched on its first whitespace-
// separated token so callers can pass either "configure" or a fuller Use
// string like "configure [flags]".
func warnDeprecatedAliasOnce(cmd *cobra.Command, oldUse, replacement string) {
	old := firstWord(oldUse)
	if old == "" {
		old = cmd.Name()
	}
	key := old + "->" + replacement
	val, _ := deprecationWarnOnce.LoadOrStore(key, &sync.Once{})
	once, ok := val.(*sync.Once)
	if !ok {
		return
	}
	once.Do(func() {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: 'entire %s' is deprecated; use 'entire %s' instead.\n", old, replacement)
	})
}

func firstWord(s string) string {
	for i := range len(s) {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i]
		}
	}
	return s
}
