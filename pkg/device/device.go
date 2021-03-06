// Copyright 2020-present Open Networking Foundation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package device contains a shim definition of Device to insulate the onos-config subsystem from
// the deprecation of onos-topo/api/device.
package device

import (
	"fmt"
	"github.com/gogo/protobuf/proto"
	"github.com/onosproject/onos-api/go/onos/topo"
	"time"
)

// ID represents device globally unique ID
type ID topo.ID

// Type represents device type
type Type string

// Role represents device role in the network
type Role string

// Device structure provide a shim for topo.Object
type Device struct {
	// globally unique device identifier; maps to Object.ID
	ID ID

	// host:port of the device
	Address string
	// device target
	Target string
	// device software version
	Version string

	// timeout indicates the device request timeout
	Timeout *time.Duration
	// credentials for connecting to the device
	Credentials Credentials
	// TLS configuration for connecting to the device
	TLS TLSConfig

	// type of the device
	Type Type
	// role for the device
	Role Role

	Protocols []*topo.ProtocolState
	// user-friendly tag
	Displayname string

	// arbitrary mapping of attribute keys/values
	Attributes map[string]string

	// revision of the underlying Object
	Revision topo.Revision
}

// Credentials is the device credentials
type Credentials struct {
	// user with which to connect to the device
	User string
	// password for connecting to the device
	Password string
}

// TLSConfig contains information pertinent to establishing a secure connection
type TLSConfig struct {
	// name of the device's CA certificate
	CaCert string
	// name of the device's certificate
	Cert string
	// name of the device's TLS key
	Key string
	// indicates whether to connect to the device over plaintext
	Plain bool
	// indicates whether to connect to the device with insecure communication
	Insecure bool
}

// ListResponse carries a single device event
type ListResponse struct {
	// type of the event
	Type ListResponseType
	// device is the device on which the event occurred
	Device *Device
}

// ListResponseType is a device event type
type ListResponseType int32

const (
	// ListResponseNONE obviously
	ListResponseNONE ListResponseType = 0
	// ListResponseADDED obviously
	ListResponseADDED ListResponseType = 1
	// ListResponseUPDATED obviously
	ListResponseUPDATED ListResponseType = 2
	// ListResponseREMOVED obviously
	ListResponseREMOVED ListResponseType = 3
)

// ListresponsetypeName - map of enumerations
var ListresponsetypeName = map[int32]string{
	0: "NONE",
	1: "ADDED",
	2: "UPDATED",
	3: "REMOVED",
}

// String - convert to string
func (x ListResponseType) String() string {
	return proto.EnumName(ListresponsetypeName, int32(x))
}

func setAttribute(o *topo.Object, k string, v string) {
	if len(v) > 0 {
		o.Attributes[k] = v
	}
}

func flag(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// ToObject converts topology object entity to local device
func ToObject(device *Device) *topo.Object {
	o := &topo.Object{
		ID:         topo.ID(device.ID),
		Revision:   device.Revision,
		Attributes: device.Attributes,
		Type:       topo.Object_ENTITY,
		Obj: &topo.Object_Entity{
			Entity: &topo.Entity{
				KindID:    topo.ID(device.Type),
				Protocols: device.Protocols,
			},
		},
	}

	if o.Attributes == nil {
		o.Attributes = make(map[string]string)
	}
	setAttribute(o, topo.Type, string(device.Type))
	setAttribute(o, topo.Role, string(device.Role))
	setAttribute(o, topo.Address, device.Address)
	setAttribute(o, topo.Target, device.Target)
	setAttribute(o, topo.Version, device.Version)
	setAttribute(o, topo.TLSInsecure, flag(device.TLS.Insecure))
	setAttribute(o, topo.TLSPlain, flag(device.TLS.Plain))
	setAttribute(o, topo.TLSInsecure, device.TLS.Key)
	setAttribute(o, topo.TLSInsecure, device.TLS.CaCert)
	setAttribute(o, topo.TLSInsecure, device.TLS.Cert)

	return o
}

// ToDevice converts local device structure to topology object entity
func ToDevice(object *topo.Object) (*Device, error) {
	if object.Type != topo.Object_ENTITY {
		return nil, fmt.Errorf("object is not a topo entity %v+", object)
	}
	version, ok := object.Attributes[topo.Version]
	if !ok {
		return nil, fmt.Errorf("topo entity %s must have 'version' attribute to work with onos-config", object.ID)
	}
	address, ok := object.Attributes[topo.Address]
	if !ok {
		return nil, fmt.Errorf("topo entity %s must have 'address' attribute to work with onos-config", object.ID)
	}
	typeKindID := Type(object.GetEntity().KindID)
	if len(typeKindID) == 0 {
		return nil, fmt.Errorf("topo entity %s must have a 'kindid' to work with onos-config", object.ID)
	}
	d := &Device{
		ID:        ID(object.ID),
		Revision:  object.Revision,
		Protocols: object.GetEntity().Protocols,
		Type:      typeKindID,
		Role:      Role(object.Attributes[topo.Role]),
		Address:   address,
		Target:    object.Attributes[topo.Target],
		Version:   version,
		TLS: TLSConfig{
			Plain:    object.Attributes[topo.TLSPlain] == "true",
			Insecure: object.Attributes[topo.TLSInsecure] == "true",
			Cert:     object.Attributes[topo.TLSCert],
			CaCert:   object.Attributes[topo.TLSCaCert],
			Key:      object.Attributes[topo.TLSKey],
		},
		Attributes: object.Attributes,
	}
	return d, nil
}
