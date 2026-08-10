package staticmdns

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// peersFile mirrors the shape of config/eebus.yaml's top-level `peers:` list, so a peers
// file can be extracted from -- or be exactly -- the main testbench config. See
// docs/01-architecture.md "Config" for the full schema; only the fields relevant to
// discovery are read here.
type peersFile struct {
	Peers []peerEntry `yaml:"peers"`
}

type peerEntry struct {
	Name  string `yaml:"name"`
	Label string `yaml:"label"`
	SKI   string `yaml:"ski"`
	Host  string `yaml:"host"`
	Port  int    `yaml:"port"`
	Path  string `yaml:"path"`
	Trust string `yaml:"trust"` // "auto" | "manual"
}

// LoadPeersFile reads a YAML file in the peers: shape and returns them as Provider-ready
// Peer values. A peer whose Trust is anything other than exactly "manual" is treated as
// auto-accept, matching config/eebus.yaml's documented default.
func LoadPeersFile(path string) ([]Peer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var pf peersFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	peers := make([]Peer, 0, len(pf.Peers))
	for i, e := range pf.Peers {
		if e.SKI == "" || e.Host == "" || e.Port == 0 {
			return nil, fmt.Errorf("peers[%d] (%s): ski, host and port are all mandatory", i, e.Name)
		}
		peers = append(peers, Peer{
			Name:       e.Name,
			SKI:        e.SKI,
			Host:       e.Host,
			Port:       e.Port,
			Path:       e.Path,
			AutoAccept: e.Trust != "manual",
		})
	}
	return peers, nil
}
