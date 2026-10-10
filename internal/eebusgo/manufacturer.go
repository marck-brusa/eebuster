package eebusgo

import (
	"github.com/enbility/eebus-go/features/client"
	spineapi "github.com/enbility/spine-go/api"
	spinemodel "github.com/enbility/spine-go/model"
)

// EntityManufacturer is the device classification one entity of a peer reports.
type EntityManufacturer struct {
	Entity     []uint            `json:"entity"`
	EntityType string            `json:"entity_type,omitempty"`
	Data       map[string]string `json:"data"`
}

// PeerManufacturer reads the manufacturer data of every entity of a connected peer that has
// a device classification server feature, requesting it from the device first. This is what
// identifies a device in a test report regardless of the use cases it advertises.
func (s *Stack) PeerManufacturer(ski string) ([]EntityManufacturer, error) {
	var remote spineapi.DeviceRemoteInterface
	for _, r := range s.service.LocalDevice().RemoteDevices() {
		if r.Ski() == ski {
			remote = r
		}
	}
	out := []EntityManufacturer{}
	var err error
	if remote == nil {
		err = &NotFoundError{Detail: "peer " + ski + " is not connected"}
	} else {
		local := s.service.LocalDevice().EntityForType(spinemodel.EntityTypeTypeCEM)
		type reader struct {
			entity spineapi.EntityRemoteInterface
			dc     *client.DeviceClassification
		}
		var readers []reader
		var requests []readRequest
		for _, entity := range remote.Entities() {
			if dc, dcErr := client.NewDeviceClassification(local, entity); dcErr == nil {
				readers = append(readers, reader{entity: entity, dc: dc})
				requests = append(requests, readRequest{feature: dc, send: dc.RequestManufacturerDetails})
			}
		}
		refresh(requests...)
		for _, r := range readers {
			if data, getErr := r.dc.GetManufacturerDetails(); getErr == nil && data != nil {
				out = append(out, EntityManufacturer{
					Entity: entityAddress(r.entity), EntityType: string(r.entity.EntityType()), Data: manufacturerMap(data),
				})
			}
		}
	}
	return out, err
}

func manufacturerMap(d *spinemodel.DeviceClassificationManufacturerDataType) map[string]string {
	out := map[string]string{}
	for key, value := range map[string]string{
		"device_name":                      deref(d.DeviceName),
		"device_code":                      deref(d.DeviceCode),
		"serial_number":                    deref(d.SerialNumber),
		"software_revision":                deref(d.SoftwareRevision),
		"hardware_revision":                deref(d.HardwareRevision),
		"vendor_name":                      deref(d.VendorName),
		"vendor_code":                      deref(d.VendorCode),
		"brand_name":                       deref(d.BrandName),
		"power_source":                     deref(d.PowerSource),
		"manufacturer_node_identification": deref(d.ManufacturerNodeIdentification),
		"manufacturer_label":               deref(d.ManufacturerLabel),
		"manufacturer_description":         deref(d.ManufacturerDescription),
	} {
		if value != "" {
			out[key] = value
		}
	}
	return out
}

func deref[T ~string](p *T) string {
	value := ""
	if p != nil {
		value = string(*p)
	}
	return value
}
