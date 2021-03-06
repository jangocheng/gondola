// +build template_compiler_debug

package template

import (
	"fmt"
	"os"
)

func (p *program) debugDump() {
	p.dumpAllTemplates(nil)
}

func compilerDebugf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
}

func compilerDebugln(args ...interface{}) {
	fmt.Fprintln(os.Stderr, args...)
}
