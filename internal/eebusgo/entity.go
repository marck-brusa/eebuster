package eebusgo

import (
	"fmt"

	eebusapi "github.com/enbility/eebus-go/api"
	spineapi "github.com/enbility/spine-go/api"
)

// EntityAmbiguousError mirrors src/facade/adapters/jsonrpc_adapter.py's EntityAmbiguousError:
// the stack supports the operation fine, the peer just has more than one qualifying entity
// and the caller must disambiguate with ?entity=.
type EntityAmbiguousError struct {
	SKI        string
	Candidates [][]uint
}

func (e *EntityAmbiguousError) Error() string {
	return fmt.Sprintf("peer %s has %d candidate entities %v; specify entity_hint to disambiguate", e.SKI, len(e.Candidates), e.Candidates)
}

// NotFoundError mirrors the plain ValueError routes_lpc.py's callers raise for an unknown SKI
// or entity -- see app.py's ValueError exception handler, which turns those into a 404
// rather than a 500.
type NotFoundError struct{ Detail string }

func (e *NotFoundError) Error() string { return e.Detail }

// resolveEntity picks the remote entity to operate on for a given use case (whose current
// RemoteEntitiesScenarios() is passed in) and peer SKI, matching
// JsonRpcAdapter.resolve_entity's precedence: an explicit hint wins outright; otherwise a
// single compatible entity is used automatically; more than one requires disambiguation.
func resolveEntity(scenarios []eebusapi.RemoteEntityScenarios, ski string, entityHint []uint) (spineapi.EntityRemoteInterface, error) {
	var candidates []spineapi.EntityRemoteInterface
	for _, rs := range scenarios {
		if rs.Entity.Device().Ski() != ski {
			continue
		}
		candidates = append(candidates, rs.Entity)
	}
	if len(candidates) == 0 {
		return nil, &NotFoundError{Detail: fmt.Sprintf("peer %s has no entity supporting this use case (not connected, or use case not advertised?)", ski)}
	}

	if entityHint != nil {
		for _, c := range candidates {
			if addressMatches(c, entityHint) {
				return c, nil
			}
		}
		return nil, &NotFoundError{Detail: fmt.Sprintf("peer %s has no matching entity at address %v", ski, entityHint)}
	}

	if len(candidates) == 1 {
		return candidates[0], nil
	}

	addrs := make([][]uint, len(candidates))
	for i, c := range candidates {
		addrs[i] = entityAddress(c)
	}
	return nil, &EntityAmbiguousError{SKI: ski, Candidates: addrs}
}

// entitiesForSKI returns every entity a use case currently has scenarios for on the given
// peer -- unlike resolveEntity, this is for aggregation across all of a peer's entities for a
// use case (e.g. every EV on a multi-connector charger), matching intelligence_snapshot()'s
// `entities(name)` helper, not the single-value REST endpoints' resolve-one-or-ambiguous
// semantics.
func entitiesForSKI(scenarios []eebusapi.RemoteEntityScenarios, ski string) []spineapi.EntityRemoteInterface {
	var out []spineapi.EntityRemoteInterface
	for _, rs := range scenarios {
		if rs.Entity.Device().Ski() == ski {
			out = append(out, rs.Entity)
		}
	}
	return out
}

func entityAddress(e spineapi.EntityInterface) []uint {
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

func addressMatches(e spineapi.EntityInterface, hint []uint) bool {
	addr := entityAddress(e)
	if len(addr) != len(hint) {
		return false
	}
	for i := range addr {
		if addr[i] != hint[i] {
			return false
		}
	}
	return true
}
