package eebusgo

import (
	"fmt"
	"sort"

	spinemodel "github.com/enbility/spine-go/model"
)

// PeerSubscription is one subscription a peer holds on a feature of ours: what it asked to
// be notified about. The server side is ours, the client side is the peer's.
type PeerSubscription struct {
	ServerEntity     []uint `json:"server_entity"`
	ServerFeature    string `json:"server_feature"`
	ClientEntity     []uint `json:"client_entity"`
	ClientEntityType string `json:"client_entity_type,omitempty"`
	ClientFeature    *uint  `json:"client_feature,omitempty"`
}

// PeerSubscriptions lists the subscriptions the peer holds on our features, plus, per
// feature type of ours, the types of the peer's entities subscribed to it. A use case that
// makes an entity watch our heartbeat or operating state shows up as that entity type under
// DeviceDiagnosis.
func (s *Stack) PeerSubscriptions(ski string) ([]PeerSubscription, map[string][]string, error) {
	out := []PeerSubscription{}
	var err error
	local := s.service.LocalDevice()
	remote := local.RemoteDeviceForSki(ski)
	if remote == nil {
		err = &NotFoundError{Detail: fmt.Sprintf("peer %s is not connected", ski)}
	} else {
		for _, entry := range local.SubscriptionManager().SubscriptionsForRemoteDevice(remote) {
			sub := PeerSubscription{}
			if entry.ServerAddress != nil {
				sub.ServerEntity = featureEntity(entry.ServerAddress)
				if f := local.FeatureByAddress(entry.ServerAddress); f != nil {
					sub.ServerFeature = string(f.Type())
				}
			}
			if entry.ClientAddress != nil {
				sub.ClientEntity = featureEntity(entry.ClientAddress)
				if entity := remote.Entity(entry.ClientAddress.Entity); entity != nil {
					sub.ClientEntityType = string(entity.EntityType())
				}
				if entry.ClientAddress.Feature != nil {
					f := uint(*entry.ClientAddress.Feature)
					sub.ClientFeature = &f
				}
			}
			out = append(out, sub)
		}
	}
	return out, subscribersByFeature(out), err
}

func featureEntity(addr *spinemodel.FeatureAddressType) []uint {
	out := make([]uint, len(addr.Entity))
	for i, v := range addr.Entity {
		out[i] = uint(v)
	}
	return out
}

// subscribersByFeature maps each of our feature types to the sorted, distinct types of the
// peer's entities that subscribed to it.
func subscribersByFeature(subs []PeerSubscription) map[string][]string {
	out := map[string][]string{}
	for _, sub := range subs {
		if sub.ServerFeature != "" && sub.ClientEntityType != "" && !contains(out[sub.ServerFeature], sub.ClientEntityType) {
			out[sub.ServerFeature] = append(out[sub.ServerFeature], sub.ClientEntityType)
		}
	}
	for _, types := range out {
		sort.Strings(types)
	}
	return out
}

func contains(list []string, item string) bool {
	found := false
	for _, v := range list {
		found = found || v == item
	}
	return found
}
