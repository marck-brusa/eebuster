package main

import "strings"

// reorderArgs lets flags and the positional argument appear in either order on the command
// line (e.g. both `run scenario.yaml -junit out.xml` and `run -junit out.xml scenario.yaml`
// work), which Go's flag package doesn't support on its own -- it stops looking for flags at
// the first positional argument. valueFlags lists flag names (without leading dashes) that
// consume the following token as their value; anything else starting with "-" is treated as
// a boolean flag consuming no following token.
func reorderArgs(args []string, valueFlags map[string]bool) []string {
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			continue // -flag=value form already carries its value
		}
		if valueFlags[name] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positionals...)
}
