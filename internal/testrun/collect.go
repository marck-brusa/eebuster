package testrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marck-brusa/eebuster/internal/report"
	"github.com/marck-brusa/eebuster/internal/scenario"
)

// maxFrameBytes bounds the raw frames one report embeds.
const maxFrameBytes = 5 << 20

// collector reads what a report records about the testbench, the device and its state.
type collector struct {
	ctx        context.Context
	base       string
	client     *http.Client
	frameBytes int
}

func newCollector(ctx context.Context, base string) *collector {
	return &collector{ctx: ctx, base: strings.TrimRight(base, "/"), client: &http.Client{Timeout: 30 * time.Second}}
}

func (c *collector) request(method, path string, out any) error {
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, nil)
	if err == nil {
		var resp *http.Response
		resp, err = c.client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				var body struct {
					Detail string `json:"detail"`
				}
				_ = json.NewDecoder(resp.Body).Decode(&body)
				err = fmt.Errorf("%s: HTTP %d %s", path, resp.StatusCode, body.Detail)
			} else {
				err = json.NewDecoder(resp.Body).Decode(out)
			}
		}
	}
	return err
}

func (c *collector) get(path string, out any) error { return c.request(http.MethodGet, path, out) }

type identification struct {
	testbench  report.Testbench
	device     report.Device
	library    report.Library
	parameters report.Parameters
	vehicles   []map[string]any
	conditions *report.Conditions
	notices    []string
}

type configView struct {
	API struct {
		Bind string `json:"bind"`
		Port int    `json:"port"`
	} `json:"api"`
	Network struct {
		Mode      string `json:"mode"`
		Interface string `json:"interface"`
	} `json:"network"`
	Peers []struct {
		Name       string         `json:"name"`
		Label      string         `json:"label"`
		SKI        string         `json:"ski"`
		Host       string         `json:"host"`
		Port       int            `json:"port"`
		Path       string         `json:"path"`
		Parameters map[string]any `json:"parameters"`
	} `json:"peers"`
	Simulator struct {
		Enabled bool `json:"enabled"`
		Devices []struct {
			ID string `json:"id"`
		} `json:"devices"`
	} `json:"simulator"`
}

type peerView struct {
	SKI           string `json:"ski"`
	Connected     bool   `json:"connected"`
	DeviceAddress string `json:"device_address"`
	Label         string `json:"label"`
	Name          string `json:"name"`
	Brand         string `json:"brand"`
	Model         string `json:"model"`
	Serial        string `json:"serial"`
	ShipID        string `json:"ship_id"`
}

// identify records the testbench, resolves and describes the device, and captures the
// parameters and the starting conditions.
func (c *collector) identify(peerName, scenariosDir string, discover bool) identification {
	var id identification
	var cfg configView
	cfgErr := c.get("/api/v1/config", &cfg)
	id.testbench = c.testbench(cfg)
	id.library = libraryOf(scenariosDir)

	var peers []peerView
	_ = c.get("/api/v1/peers", &peers)
	ski, configured := resolvePeer(peerName, cfg, peers)
	id.device = report.Device{Peer: peerName, SKI: ski, Entities: []report.Entity{}, UseCases: []report.AdvertisedUseCase{}, Manufacturer: []report.EntityManufacturer{}}
	var configuredParams map[string]any
	if configured >= 0 {
		p := cfg.Peers[configured]
		id.device.Label, id.device.Host, id.device.Port, id.device.Path = p.Label, p.Host, p.Port, p.Path
		id.device.AddressSource = "configured peer"
		configuredParams = p.Parameters
	}
	if cfgErr != nil {
		id.device.Errors = append(id.device.Errors, "config: "+cfgErr.Error())
	}
	for _, p := range peers {
		if strings.EqualFold(p.SKI, ski) && ski != "" {
			id.device.Connected = p.Connected
			id.device.DeviceAddress, id.device.ShipID, id.device.Name = p.DeviceAddress, p.ShipID, p.Name
			id.device.Brand, id.device.Model, id.device.Serial = p.Brand, p.Model, p.Serial
			if id.device.Label == "" {
				id.device.Label = p.Label
			}
		}
	}
	if ski == "" {
		id.notices = append(id.notices, fmt.Sprintf("The peer %q could not be resolved to a device; every test case that targets it fails.", peerName))
	} else if !id.device.Connected {
		id.notices = append(id.notices, "The device was not connected when the run started.")
	} else {
		c.describe(&id.device, discover)
	}
	id.parameters = c.parameters(configuredParams, id.device)
	id.vehicles, id.conditions = c.vehiclesAndConditions(id.device)
	return id
}

// resolvePeer finds the device a run targets: a configured peer name, a literal SKI, or --
// with no peers configured -- the only connected peer. It returns the SKI and the index of
// the configured peer, or -1.
func resolvePeer(name string, cfg configView, peers []peerView) (string, int) {
	ski, index := "", -1
	for i, p := range cfg.Peers {
		if index < 0 && (p.Name == name || strings.EqualFold(p.SKI, name)) {
			ski, index = strings.ToLower(p.SKI), i
		}
	}
	if index < 0 && len(name) == 40 {
		ski = strings.ToLower(name)
	}
	if ski == "" && len(cfg.Peers) == 0 {
		var connected []string
		for _, p := range peers {
			if p.Connected {
				connected = append(connected, p.SKI)
			}
		}
		if len(connected) == 1 {
			ski = connected[0]
		}
	}
	return ski, index
}

func (c *collector) testbench(cfg configView) report.Testbench {
	var version struct {
		Version   string            `json:"version"`
		Modules   map[string]string `json:"modules"`
		GoVersion string            `json:"go_version"`
		Host      string            `json:"host"`
		Platform  string            `json:"platform"`
	}
	_ = c.get("/api/v1/version", &version)
	t := report.Testbench{
		Version: version.Version, Modules: version.Modules, GoVersion: version.GoVersion, Host: version.Host, Platform: version.Platform,
		NetworkMode: cfg.Network.Mode, Interface: cfg.Network.Interface,
	}
	if cfg.API.Port > 0 {
		t.API = fmt.Sprintf("%s:%d", cfg.API.Bind, cfg.API.Port)
	}
	var identity map[string]map[string]string
	if c.get("/api/v1/identity", &identity) == nil {
		for _, stack := range sortedKeys(identity) {
			if t.SKI == "" {
				t.SKI = identity[stack]["ski"]
			}
		}
	}
	var network struct {
		ShipPort  int `json:"ship_port"`
		Announced []struct {
			IP        string `json:"ip"`
			Interface string `json:"interface"`
		} `json:"announced"`
	}
	if c.get("/api/v1/diagnostics/network", &network) == nil {
		t.ShipPort = network.ShipPort
		for _, a := range network.Announced {
			t.Announced = append(t.Announced, strings.TrimSpace(a.IP+" "+bracket(a.Interface)))
		}
	}
	if cfg.Simulator.Enabled {
		for _, d := range cfg.Simulator.Devices {
			t.Simulators = append(t.Simulators, d.ID)
		}
	}
	return t
}

func bracket(s string) string {
	text := ""
	if s != "" {
		text = "(" + s + ")"
	}
	return text
}

// describe fills in what the connected device reports about itself. discover adds a short
// mDNS browse for the addresses the device announces.
func (c *collector) describe(d *report.Device, discover bool) {
	var profile struct {
		DeviceType     string          `json:"device_type"`
		FeatureSet     string          `json:"feature_set"`
		Entities       []report.Entity `json:"entities"`
		UseCaseDetails []struct {
			Name      string `json:"name"`
			Acronym   string `json:"acronym"`
			Title     string `json:"title"`
			Actor     string `json:"actor"`
			Version   string `json:"version"`
			Scenarios []uint `json:"scenarios"`
			Address   []uint `json:"address"`
			Available bool   `json:"available"`
		} `json:"use_case_details"`
	}
	if err := c.get("/api/v1/peers/"+d.SKI+"/profile", &profile); err != nil {
		d.Errors = append(d.Errors, "profile: "+err.Error())
	} else {
		d.DeviceType, d.FeatureSet = profile.DeviceType, profile.FeatureSet
		if profile.Entities != nil {
			d.Entities = profile.Entities
		}
		for _, u := range profile.UseCaseDetails {
			d.UseCases = append(d.UseCases, report.AdvertisedUseCase{
				Name: u.Name, Acronym: u.Acronym, Title: u.Title, Actor: u.Actor, Version: u.Version,
				Scenarios: u.Scenarios, Address: u.Address, Available: u.Available,
			})
		}
	}
	var manufacturer struct {
		Entities []report.EntityManufacturer `json:"entities"`
	}
	if err := c.get("/api/v1/peers/"+d.SKI+"/manufacturer", &manufacturer); err != nil {
		d.Errors = append(d.Errors, "manufacturer data: "+err.Error())
	} else if manufacturer.Entities != nil {
		d.Manufacturer = manufacturer.Entities
	}
	if advertises(*d, "evseCommissioningAndConfiguration") {
		var evse map[string]any
		if err := c.get("/api/v1/evsecc/"+d.SKI, &evse); err != nil {
			d.Errors = append(d.Errors, "EVSE identity: "+err.Error())
		} else {
			d.EVSE = evse
		}
	}
	var discovered struct {
		Found []struct {
			SKI       string   `json:"ski"`
			Host      string   `json:"host"`
			Port      int      `json:"port"`
			Addresses []string `json:"addresses"`
			Type      string   `json:"type"`
		} `json:"found"`
	}
	if discover && c.request(http.MethodPost, "/api/v1/discover?timeout_s=2", &discovered) == nil {
		for _, f := range discovered.Found {
			if strings.EqualFold(f.SKI, d.SKI) {
				d.Addresses, d.Type = f.Addresses, f.Type
				if d.Host == "" {
					d.Host, d.Port = f.Host, f.Port
				}
				d.AddressSource = strings.TrimPrefix(d.AddressSource+"; mDNS announcement", "; ")
			}
		}
	}
}

func advertises(d report.Device, name string) bool {
	found := false
	for _, u := range d.UseCases {
		found = found || (u.Name == name && u.Available)
	}
	return found
}

// vehiclesAndConditions captures the vehicles and the device state.
func (c *collector) vehiclesAndConditions(d report.Device) ([]map[string]any, *report.Conditions) {
	vehicles := []map[string]any{}
	conditions := &report.Conditions{At: time.Now()}
	var peers []peerView
	if c.get("/api/v1/peers", &peers) == nil {
		for _, p := range peers {
			conditions.Connected = conditions.Connected || (strings.EqualFold(p.SKI, d.SKI) && p.Connected && d.SKI != "")
		}
	}
	if conditions.Connected {
		var snapshot struct {
			Power struct {
				ConsumptionW *float64 `json:"consumption_w"`
			} `json:"power"`
			Limits struct {
				FailsafeW   *float64 `json:"failsafe_w"`
				Consumption *struct {
					ValueW   float64 `json:"value_w"`
					IsActive bool    `json:"is_active"`
				} `json:"consumption"`
			} `json:"limits"`
			EV struct {
				ConnectedCount int              `json:"connected_count"`
				ChargingCount  int              `json:"charging_count"`
				Vehicles       []map[string]any `json:"vehicles"`
			} `json:"ev"`
		}
		if c.get("/api/v1/energy/"+d.SKI+"/snapshot", &snapshot) == nil {
			conditions.ConsumptionW, conditions.FailsafeW = snapshot.Power.ConsumptionW, snapshot.Limits.FailsafeW
			conditions.VehiclesConnected, conditions.VehiclesCharging = snapshot.EV.ConnectedCount, snapshot.EV.ChargingCount
			if limit := snapshot.Limits.Consumption; limit != nil {
				active, value := limit.IsActive, limit.ValueW
				conditions.LimitActive, conditions.LimitW = &active, &value
			}
			if snapshot.EV.Vehicles != nil {
				vehicles = snapshot.EV.Vehicles
			}
		}
		if advertises(d, "limitationOfPowerConsumption") {
			var failsafe struct {
				Duration string `json:"duration"`
			}
			if c.get("/api/v1/lpc/"+d.SKI+"/failsafe", &failsafe) == nil {
				conditions.FailsafeDuration = failsafe.Duration
			}
			var heartbeat struct {
				Within  *bool    `json:"within_duration"`
				Timeout *float64 `json:"heartbeat_timeout_s"`
			}
			if c.get("/api/v1/lpc/"+d.SKI+"/heartbeat", &heartbeat) == nil {
				conditions.HeartbeatWithin, conditions.HeartbeatTimeoutS = heartbeat.Within, heartbeat.Timeout
			}
		}
	}
	conditions.TraceSeq = c.traceSeq()
	conditions.EventSeq = c.eventSeq()
	var summary struct {
		Errors   int `json:"errors"`
		Warnings int `json:"warnings"`
	}
	if c.get("/api/v1/trace/summary", &summary) == nil {
		conditions.ConformanceErrors, conditions.ConformanceWarnings = summary.Errors, summary.Warnings
	}
	return vehicles, conditions
}

func (c *collector) traceSeq() int64 {
	var trace struct {
		Latest int64 `json:"latest_seq"`
	}
	_ = c.get("/api/v1/trace?limit=1", &trace)
	return trace.Latest
}

func (c *collector) eventSeq() int64 {
	var events []struct {
		Seq int64 `json:"seq"`
	}
	seq := int64(0)
	if c.get("/api/v1/events/recent?limit=1", &events) == nil && len(events) > 0 {
		seq = events[0].Seq
	}
	return seq
}

// awaitConnected waits until the device is connected again, or the timeout passes.
func (c *collector) awaitConnected(ski string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	connected := false
	for !connected && time.Now().Before(deadline) {
		var peers []peerView
		if c.get("/api/v1/peers", &peers) == nil {
			for _, p := range peers {
				connected = connected || (strings.EqualFold(p.SKI, ski) && p.Connected)
			}
		}
		if !connected {
			time.Sleep(time.Second)
		}
	}
	return connected
}

// frames returns the raw frames of a test case window when the run embeds frames, within
// the overall byte budget; the rest are counted as dropped.
func (c *collector) frames(window *scenario.SeqRange, include bool) ([]map[string]any, int) {
	var out []map[string]any
	dropped := 0
	if include && window != nil && window.To >= window.From {
		var page struct {
			Entries []map[string]any `json:"entries"`
		}
		query := url.Values{"after": {fmt.Sprint(window.From - 1)}, "limit": {"2000"}, "raw": {"1"}}
		if c.get("/api/v1/trace?"+query.Encode(), &page) == nil {
			for _, e := range page.Entries {
				seq, _ := e["seq"].(float64)
				raw, _ := e["raw"].(string)
				if int64(seq) <= window.To && c.frameBytes+len(raw) <= maxFrameBytes {
					c.frameBytes += len(raw)
					out = append(out, e)
				} else if int64(seq) <= window.To {
					dropped++
				}
			}
		}
	}
	return out, dropped
}

// libraryOf hashes the scenario files, so two runs with the same hash ran the same criteria.
func libraryOf(dir string) report.Library {
	lib := report.Library{Dir: dir}
	if abs, err := filepath.Abs(dir); err == nil {
		lib.Dir = abs
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil {
			lib.Files++
			h.Write([]byte(filepath.Base(p)))
			h.Write([]byte{0})
			h.Write(data)
			h.Write([]byte{0})
		}
	}
	lib.SHA256 = hex.EncodeToString(h.Sum(nil))
	return lib
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
