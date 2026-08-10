// Command eebus-testbench is the portable, single-binary EEBUS test controller: config,
// identity, the embedded eebus-go stack, the REST dashboard, and the scenario runner all in
// one process. openeebus-hems and EEBusTracer are out of scope for this rewrite -- see docs/.
//
// Usage:
//
//	eebus-testbench [serve] [-config path] [-data-dir dir]
//	eebus-testbench run <scenario.yaml> [-junit path] [-base-url url]
//	eebus-testbench run-all <dir> [-junit path] [-base-url url]
//	eebus-testbench firewall [-port 4712] [-add] [-remove]
package main

import (
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !isFlag(args[0]) {
		cmd = args[0]
		args = args[1:]
	}

	switch cmd {
	case "serve":
		runServe(args)
	case "run":
		runScenarioCmd(args)
	case "run-all":
		runAllScenariosCmd(args)
	case "firewall":
		runFirewall(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (want serve, run, run-all, or firewall)\n", cmd)
		os.Exit(2)
	}
}

func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }
