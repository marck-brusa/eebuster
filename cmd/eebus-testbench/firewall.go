package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// firewallRuleName is the display name used for the inbound rule, so `firewall -remove` can find
// exactly the rule this tool created and nothing else.
const firewallRuleName = "EEBUS testbench SHIP"

// inboundCommands returns the platform's commands to allow inbound TCP on port, most-likely
// first. Returned as strings to print rather than executed, except on Windows via -add.
func inboundCommands(port int) []string {
	p := strconv.Itoa(port)
	switch runtime.GOOS {
	case "windows":
		return []string{
			`netsh advfirewall firewall add rule name="` + firewallRuleName + `" dir=in action=allow protocol=TCP localport=` + p,
		}
	case "darwin":
		return []string{
			"sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add " + selfPath(),
			"sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp " + selfPath(),
		}
	default:
		return []string{
			"sudo ufw allow " + p + "/tcp",
			"sudo firewall-cmd --add-port=" + p + "/tcp",
			"sudo nft insert rule inet filter INPUT tcp dport " + p + " counter accept",
			"sudo iptables -I INPUT -p tcp --dport " + p + " -j ACCEPT",
		}
	}
}

func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "eebus-testbench"
	}
	return exe
}

// logInboundReachabilityHint warns that SHIP needs an *inbound* TCP port, which is the half of
// pairing people do not expect. Discovery is multicast UDP and works even when TCP is blocked, so
// a device lists us happily in its scan and then times out connecting -- with the failure visible
// only in the device's log, never ours.
//
// This is printed unconditionally rather than probed, because a process cannot test its own
// inbound reachability from inside the host: binding the port succeeds whether or not the
// firewall drops packets arriving on it. Parsing `netsh advfirewall show rule` would be the
// alternative and it is localised, so it reports nothing useful on a German or French Windows.
func logInboundReachabilityHint(shipPort int) {
	switch runtime.GOOS {
	case "windows":
		log.Printf("firewall: SHIP needs INBOUND TCP %d. Windows Firewall blocks inbound connections by default on any network "+
			"profiled as Public, which is what a device's own Wi-Fi access point is usually classified as.", shipPort)
		log.Printf("firewall: symptom -- the device discovers us over mDNS but its connect attempt times out, and only its log shows it.")
		log.Printf("firewall: fix (Administrator PowerShell or cmd): %s", inboundCommands(shipPort)[0])
		log.Printf("firewall: or run: %s firewall -add", exeName())
	case "darwin":
		log.Printf("firewall: SHIP needs INBOUND TCP %d. If the macOS application firewall is on, allow this binary: %s firewall",
			shipPort, exeName())
	default:
		log.Printf("firewall: SHIP needs INBOUND TCP %d -- if a peer discovers us but cannot connect, check the host firewall (%s firewall)",
			shipPort, exeName())
	}
}

func exeName() string {
	if runtime.GOOS == "windows" {
		return "eebus-testbench.exe"
	}
	return "./eebus-testbench"
}

// runFirewall implements the `firewall` subcommand: with no flags it prints what to run, with
// -add it actually applies the rule on Windows. Executing a system-modifying command is
// deliberately opt-in and never happens as a side effect of `serve`.
func runFirewall(args []string) {
	fs := flag.NewFlagSet("firewall", flag.ExitOnError)
	port := fs.Int("port", 4712, "SHIP port to allow inbound")
	add := fs.Bool("add", false, "actually add the inbound allow rule (Windows only; needs Administrator)")
	remove := fs.Bool("remove", false, "remove the rule added by -add (Windows only; needs Administrator)")
	fs.Parse(args)

	if *add || *remove {
		if runtime.GOOS != "windows" {
			fmt.Printf("-add/-remove is Windows-only, because a single portable command exists there and not on %s.\n", runtime.GOOS)
			fmt.Printf("Run whichever of these matches your firewall:\n\n")
			for _, c := range inboundCommands(*port) {
				fmt.Printf("  %s\n", c)
			}
			os.Exit(1)
		}
		applyWindowsRule(*port, *remove)
		return
	}

	fmt.Printf("SHIP needs an INBOUND TCP allow rule on port %d.\n\n", *port)
	fmt.Printf("Why: mDNS discovery is multicast UDP and works even when TCP is blocked, so a device\n")
	fmt.Printf("will list this testbench in its scan and then time out connecting to it. The timeout\n")
	fmt.Printf("appears only in the device's log, which is why this looks like a pairing bug.\n\n")

	if runtime.GOOS == "windows" {
		fmt.Printf("Run in an Administrator prompt:\n\n  %s\n\n", inboundCommands(*port)[0])
		fmt.Printf("Or let this tool do it (it will ask Windows for elevation):\n\n  %s firewall -add\n\n", exeName())
		fmt.Printf("Note: Windows applies the Public profile to most Wi-Fi networks, including a\n")
		fmt.Printf("device's own access point, and blocks inbound connections there by default.\n")
		return
	}
	fmt.Printf("Run whichever matches your firewall:\n\n")
	for _, c := range inboundCommands(*port) {
		fmt.Printf("  %s\n", c)
	}
}

// applyWindowsRule runs netsh. Output is echoed verbatim rather than parsed: netsh is localised,
// so the only trustworthy signal is the exit status, and the user should see whatever it said.
func applyWindowsRule(port int, remove bool) {
	var cmd *exec.Cmd
	if remove {
		cmd = exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+firewallRuleName)
	} else {
		cmd = exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
			"name="+firewallRuleName, "dir=in", "action=allow", "protocol=TCP",
			"localport="+strconv.Itoa(port))
	}

	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if text != "" {
		fmt.Println(text)
	}
	if err != nil {
		fmt.Printf("\nnetsh failed: %v\n", err)
		fmt.Printf("This almost always means the prompt is not elevated. Right-click Command Prompt or\n")
		fmt.Printf("PowerShell, choose \"Run as administrator\", and run:\n\n  %s\n", inboundCommands(port)[0])
		os.Exit(1)
	}
	action := "added"
	if remove {
		action = "removed"
	}
	fmt.Printf("\nInbound TCP %d %s (%q).\n", port, action, firewallRuleName)
}
