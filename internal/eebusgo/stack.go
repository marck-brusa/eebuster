package eebusgo

import (
	spineapi "github.com/enbility/spine-go/api"
)

// EntityInfo is the JSON-facing shape for one remote entity, matching what
// src/facade/adapters/jsonrpc_adapter.py's entities() returned over the old RPC wire, now
// read directly off the typed SPINE object instead of round-tripping call/Entities.
type EntityInfo struct {
	Address []uint `json:"address"`
	Type    string `json:"type"`
}

// PeerInfo is the JSON-facing shape for one connected remote device, matching
// src/facade/adapters/base.py's PeerInfo dataclass, plus the announced identity fields
// (name/brand/model/serial) merged in from the mDNS metadata cache -- see DeviceMeta for why
// those cannot come from the connection itself.
type PeerInfo struct {
	SKI           string       `json:"ski"`
	Connected     bool         `json:"connected"`
	Trusted       bool         `json:"trusted"`
	DeviceAddress string       `json:"device_address,omitempty"`
	Entities      []EntityInfo `json:"entities"`

	// Label is the best available human name, so a caller never has to render a bare SKI.
	Label  string `json:"label,omitempty"`
	Name   string `json:"name,omitempty"`
	Brand  string `json:"brand,omitempty"`
	Model  string `json:"model,omitempty"`
	Serial string `json:"serial,omitempty"`
	ShipID string `json:"ship_id,omitempty"`
}

// Peers lists every remote device the SPINE local device currently knows about. Direct and
// typed: no RPC round trip, no "result[0] or []" array-position bookkeeping -- see
// docs/10-eebus-go.md "Private control plane" for what this replaces.
func (s *Stack) Peers() []PeerInfo {
	remotes := s.service.LocalDevice().RemoteDevices()
	out := make([]PeerInfo, 0, len(remotes))
	for _, remote := range remotes {
		entities := remote.Entities()
		entityInfos := make([]EntityInfo, 0, len(entities))
		for _, e := range entities {
			entityInfos = append(entityInfos, EntityInfo{
				Address: addressUints(e),
				Type:    string(e.EntityType()),
			})
		}
		meta := s.MetaFor(remote.Ski())
		out = append(out, PeerInfo{
			SKI:           remote.Ski(),
			Connected:     true,
			Trusted:       true,
			DeviceAddress: deviceAddressString(remote),
			Entities:      entityInfos,
			Label:         displayName(meta.Brand, meta.Model, meta.Serial, meta.Name, remote.Ski()),
			Name:          meta.Name,
			Brand:         meta.Brand,
			Model:         meta.Model,
			Serial:        meta.Serial,
			ShipID:        meta.ShipID,
		})
	}
	return out
}

// UseCaseSupport is one entry of a UseCaseEntry's useCaseSupport list, matching SPINE's own
// model.UseCaseSupportType JSON tags (useCaseName/useCaseAvailable) -- what a peer itself
// advertises, not our own registered use cases.
type UseCaseSupport struct {
	UseCaseName      string `json:"useCaseName"`
	UseCaseAvailable bool   `json:"useCaseAvailable"`
}

// UseCaseEntry is the JSON-facing shape of one model.UseCaseInformationDataType, matching
// JsonRpcAdapter.use_cases()'s wire shape exactly: address plus a nested useCaseSupport
// list, NOT a flattened useCaseName/useCaseAvailable pair. This nesting is exactly what
// cli/scenario.py's _missing_requirements and examples/explore_peer.py both parse
// (`for entry in raw_use_cases for support in entry.get("useCaseSupport") or []`) -- an
// earlier flat version of this type silently broke both of those real REST clients, caught
// by actually running explore_peer.py against a live instance, not by reading the Go code.
type UseCaseEntry struct {
	Address        []uint           `json:"address,omitempty"`
	UseCaseSupport []UseCaseSupport `json:"useCaseSupport"`
}

// PeerUseCases lists the use cases a connected peer advertises. Empty if the SKI isn't
// currently connected.
func (s *Stack) PeerUseCases(ski string) []UseCaseEntry {
	for _, remote := range s.service.LocalDevice().RemoteDevices() {
		if remote.Ski() != ski {
			continue
		}
		raw := remote.UseCases()
		out := make([]UseCaseEntry, 0, len(raw))
		for _, entry := range raw {
			var addr []uint
			if entry.Address != nil {
				for _, v := range entry.Address.Entity {
					addr = append(addr, uint(v))
				}
			}
			support := make([]UseCaseSupport, 0, len(entry.UseCaseSupport))
			for _, s := range entry.UseCaseSupport {
				available := s.UseCaseAvailable == nil || *s.UseCaseAvailable
				name := ""
				if s.UseCaseName != nil {
					name = string(*s.UseCaseName)
				}
				support = append(support, UseCaseSupport{UseCaseName: name, UseCaseAvailable: available})
			}
			out = append(out, UseCaseEntry{Address: addr, UseCaseSupport: support})
		}
		return out
	}
	// A bare nil here would JSON-encode as `null`, not `[]` -- matching Python's use_cases()
	// returning [] when the device isn't connected, not None. Confirmed by actually running
	// explore_peer.py against a live instance: `for entry in null` is a Python TypeError, not
	// the empty-loop no-op an empty list gives.
	return []UseCaseEntry{}
}

// PeerProfile is the JSON shape for GET /peers/{ski}/profile: connected state, entity types,
// and advertised use cases.
type PeerProfile struct {
	SKI           string       `json:"ski"`
	Connected     bool         `json:"connected"`
	Trusted       bool         `json:"trusted"`
	DeviceAddress string       `json:"device_address,omitempty"`
	DeviceType    string       `json:"device_type,omitempty"`
	FeatureSet    string       `json:"feature_set,omitempty"`
	Entities      []EntityInfo `json:"entities"`
	// UseCases is the flat list of advertised use-case names, which is what a caller almost
	// always wants ("does this peer do LPC?"). RawUseCases keeps the nested per-entity shape
	// for callers that need to know *which* entity advertised what. Both are always present:
	// omitting the flat one was a regression that made device-profile-discovery assert on a
	// field that did not exist.
	UseCases    []string       `json:"use_cases"`
	RawUseCases []UseCaseEntry `json:"raw_use_cases"`
}

// flattenUseCaseNames reduces the nested per-entity advertisement to the distinct names that
// are actually available, preserving first-seen order so output is stable between calls.
func flattenUseCaseNames(entries []UseCaseEntry) []string {
	seen := map[string]bool{}
	// Never nil: an empty JSON array is iterable in every client language, `null` is not.
	names := []string{}
	for _, entry := range entries {
		for _, support := range entry.UseCaseSupport {
			if !support.UseCaseAvailable || support.UseCaseName == "" || seen[support.UseCaseName] {
				continue
			}
			seen[support.UseCaseName] = true
			names = append(names, support.UseCaseName)
		}
	}
	return names
}

// PeerProfile returns a NotFoundError if ski isn't currently connected -- a profile for an
// unreachable peer is meaningless, matching peer_profile()'s own ValueError in that case.
func (s *Stack) PeerProfile(ski string) (PeerProfile, error) {
	for _, remote := range s.service.LocalDevice().RemoteDevices() {
		if remote.Ski() != ski {
			continue
		}
		entities := remote.Entities()
		entityInfos := make([]EntityInfo, 0, len(entities))
		for _, e := range entities {
			entityInfos = append(entityInfos, EntityInfo{Address: addressUints(e), Type: string(e.EntityType())})
		}
		deviceType := ""
		if dt := remote.DeviceType(); dt != nil {
			deviceType = string(*dt)
		}
		featureSet := ""
		if fs := remote.FeatureSet(); fs != nil {
			featureSet = string(*fs)
		}
		raw := s.PeerUseCases(ski)
		return PeerProfile{
			SKI: ski, Connected: true, Trusted: true,
			DeviceAddress: deviceAddressString(remote),
			DeviceType:    deviceType,
			FeatureSet:    featureSet,
			Entities:      entityInfos,
			UseCases:      flattenUseCaseNames(raw),
			RawUseCases:   raw,
		}, nil
	}
	return PeerProfile{}, &NotFoundError{Detail: "peer " + ski + " is not connected"}
}

func addressUints(e spineapi.EntityInterface) []uint {
	addr := e.Address()
	if addr == nil {
		return nil
	}
	out := make([]uint, len(addr.Entity))
	for i, v := range addr.Entity {
		out[i] = uint(v)
	}
	return out
}

func deviceAddressString(d spineapi.DeviceRemoteInterface) string {
	addr := d.Address()
	if addr == nil {
		return ""
	}
	return string(*addr)
}
