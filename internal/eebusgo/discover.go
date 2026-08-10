package eebusgo

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	shipapi "github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mdns"
)

func orAll(interfaceName string) string {
	if interfaceName == "" {
		return "*"
	}
	return interfaceName
}

// resolveInterfaces turns a network.interface value into the []net.Interface ship-go's
// zeroconf provider wants: blank or "*" means "every usable interface" (which that provider
// represents as an empty slice), otherwise one name or a comma-separated list. Shared by
// Discover and the hybrid provider in New so both honour the same config field identically.
func resolveInterfaces(interfaceName string) ([]net.Interface, error) {
	if interfaceName == "" || interfaceName == "*" {
		return nil, nil
	}
	var ifaces []net.Interface
	for _, name := range strings.Split(interfaceName, ",") {
		iface, err := net.InterfaceByName(strings.TrimSpace(name))
		if err != nil {
			return nil, fmt.Errorf("interface %q: %w", name, err)
		}
		ifaces = append(ifaces, *iface)
	}
	return ifaces, nil
}

// DiscoveredService is one _ship._tcp instance seen on the network, matching
// EEBusTracer's discover subcommand's shape closely enough for the dashboard's Network tab
// -- but produced by ship-go's own zeroconf browser directly, since EEBusTracer was cut from
// this rewrite's scope.
type DiscoveredService struct {
	Name      string   `json:"name"`
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	Addresses []string `json:"addresses"`
	SKI       string   `json:"ski,omitempty"`
	Brand     string   `json:"brand,omitempty"`
	Model     string   `json:"model,omitempty"`
	// Serial is its own SHIP TXT field ("serial=", SHIP Requirements for Installation Process
	// V1.0.0), NOT part of the zeroconf instance name -- worth surfacing separately because it
	// is the field that actually identifies *which unit* you are looking at when several
	// identical models answer a scan.
	Serial string `json:"serial,omitempty"`
	// Type is the SPINE device type TXT field ("type="), e.g. ChargingStation.
	Type string `json:"type,omitempty"`
	// ShipID is the "id=" TXT field. eebus-go generates it as "Brand-Model-Serial" (see
	// api.Configuration.generateIdentifier), so for eebus-go based devices it usually embeds
	// the serial -- but that is a convention of the announcing implementation, not a guarantee,
	// which is why Serial above is read from its own field rather than parsed out of this.
	ShipID string `json:"ship_id,omitempty"`
}

// DisplayName is the friendliest label available for a device, preferring real identity fields
// over the raw SKI. Falls back through brand/model/serial -> instance name -> SKI so the UI
// never has to show a bare 40-hex string when something better exists.
func (d DiscoveredService) DisplayName() string {
	return displayName(d.Brand, d.Model, d.Serial, d.Name, d.SKI)
}

// unescapeInstanceName undoes DNS-SD instance-name escaping (RFC 6763 4.3): a literal space
// arrives as `\ `, a dot as `\.`, a backslash as `\\`, and other bytes may arrive as a `\DDD`
// three-digit decimal escape. Real devices hit this constantly -- any device whose model name
// contains spaces announces as e.g. `Wallbox\ Model\ One`, and showing those backslashes
// verbatim in the dashboard looks broken.
func unescapeInstanceName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		// `\DDD` decimal escape
		if i+3 < len(s) && isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
			n := int(s[i+1]-'0')*100 + int(s[i+2]-'0')*10 + int(s[i+3]-'0')
			if n <= 255 {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		// `\X` -- emit X literally; a trailing lone backslash is emitted as-is.
		if i+1 < len(s) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte('\\')
	}
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func displayName(brand, model, serial, name, ski string) string {
	label := strings.TrimSpace(strings.TrimSpace(brand) + " " + strings.TrimSpace(model))
	if label != "" {
		if serial != "" {
			return label + " (" + serial + ")"
		}
		return label
	}
	if name != "" {
		return unescapeInstanceName(name)
	}
	if serial != "" {
		return serial
	}
	return ski
}

// Discover runs a real, standalone zeroconf browse for _ship._tcp for the given duration and
// returns whatever it saw. Independent of the running Service/Hub entirely -- this can run
// in either static or mdns network mode, and doesn't touch the primary stack's own mDNS
// state. interfaceName follows the same convention as network.interface: blank or "*" means
// every usable interface, otherwise one name or a comma-separated list.
//
// Real _ship._tcp multicast only reaches devices on the same broadcast domain as the process
// doing the browsing -- under WSL2 or Docker Desktop that's the virtual switch, not the
// physical LAN (see docs/01-architecture.md "Connectivity"). Expect an empty result in those
// environments; that's the network boundary, not a bug in this function.
func Discover(interfaceName string, timeout time.Duration) ([]DiscoveredService, error) {
	log.Printf("mdns: doing mDNS discovery (interface=%q, %s)...", orAll(interfaceName), timeout)
	ifaces, err := resolveInterfaces(interfaceName)
	if err != nil {
		return nil, err
	}

	provider := mdns.NewZeroconfProvider(ifaces)

	var mu sync.Mutex
	// Keyed by SKI, not by zeroconf instance name: the same physical device re-announces
	// under several different instance names (seen in practice: a single real charging
	// station showed up as "<model>", "<model> #2", "#3", etc., all with the same
	// SKI) -- deduping by name alone left visible duplicates of one device in the result.
	// Falls back to the instance name only for the rare entry with no ski TXT field at all.
	found := map[string]*DiscoveredService{}
	cb := func(elements map[string]string, name, host, _ string, addresses []net.IP, port int, remove bool) {
		mu.Lock()
		defer mu.Unlock()
		key := elements["ski"]
		if key == "" {
			key = name
		}
		if remove {
			delete(found, key)
			return
		}
		addrs := make([]string, 0, len(addresses))
		for _, a := range addresses {
			s := a.String()
			dup := false
			for _, existing := range addrs {
				if existing == s {
					dup = true
					break
				}
			}
			if !dup {
				addrs = append(addrs, s)
			}
		}
		if existing, ok := found[key]; ok {
			// Same SKI seen again, possibly under a different instance name/address --
			// merge addresses rather than clobbering, keep the first-seen name stable.
			for _, a := range addrs {
				dup := false
				for _, e := range existing.Addresses {
					if e == a {
						dup = true
						break
					}
				}
				if !dup {
					existing.Addresses = append(existing.Addresses, a)
				}
			}
			existing.Port = port
			return
		}
		found[key] = &DiscoveredService{
			Name: unescapeInstanceName(name), Host: host, Port: port, Addresses: addrs,
			SKI: elements["ski"], Brand: elements["brand"], Model: elements["model"],
			Serial: elements["serial"], Type: elements["type"], ShipID: elements["id"],
		}
	}

	if !provider.Start(shipapi.PairingModeOff, false, cb) {
		return nil, fmt.Errorf("failed to start zeroconf browse")
	}
	time.Sleep(timeout)
	provider.Shutdown()

	mu.Lock()
	defer mu.Unlock()
	out := make([]DiscoveredService, 0, len(found))
	for _, svc := range found {
		out = append(out, *svc)
	}
	log.Printf("mdns: discovery finished, %d device(s) found", len(out))
	return out, nil
}
