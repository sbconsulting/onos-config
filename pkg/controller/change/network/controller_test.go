// Copyright 2019-present Open Networking Foundation.
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

package network

import (
	"context"
	"github.com/golang/mock/gomock"
	types "github.com/onosproject/onos-api/go/onos/config/change"
	devicechange "github.com/onosproject/onos-api/go/onos/config/change/device"
	networkchange "github.com/onosproject/onos-api/go/onos/config/change/network"
	"github.com/onosproject/onos-api/go/onos/config/device"
	devicechangecontroller "github.com/onosproject/onos-config/pkg/controller/change/device"
	topodevice "github.com/onosproject/onos-config/pkg/device"
	"github.com/onosproject/onos-config/pkg/southbound"
	devicechangestore "github.com/onosproject/onos-config/pkg/store/change/device"
	networkchangestore "github.com/onosproject/onos-config/pkg/store/change/network"
	devicestore "github.com/onosproject/onos-config/pkg/store/device"
	"github.com/onosproject/onos-config/pkg/store/device/cache"
	leadershipstore "github.com/onosproject/onos-config/pkg/store/leadership"
	mastershipstore "github.com/onosproject/onos-config/pkg/store/mastership"
	"github.com/onosproject/onos-config/pkg/store/stream"
	southboundtest "github.com/onosproject/onos-config/pkg/test/mocks/southbound"
	"github.com/onosproject/onos-lib-go/pkg/controller"
	"github.com/openconfig/gnmi/proto/gnmi"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
	"sync"
	"testing"
	"time"
)

func setupControllers(t *testing.T, networkChanges networkchangestore.Store,
	deviceChanges devicechangestore.Store, devices devicestore.Store,
	deviceCache cache.Cache,
	leadershipStore leadershipstore.Store, mastershipStore mastershipstore.Store) (
	*controller.Controller, *controller.Controller) {

	networkChangeController := NewController(leadershipStore, deviceCache, devices, networkChanges, deviceChanges)
	assert.NotNil(t, networkChangeController)

	deviceChangeController := devicechangecontroller.NewController(mastershipStore, devices, deviceCache, deviceChanges)
	assert.NotNil(t, deviceChangeController)

	return networkChangeController, deviceChangeController
}

func newMockTarget(t *testing.T, ctrl *gomock.Controller, id topodevice.ID) (*southboundtest.MockTargetIf, context.CancelFunc) {
	mockTargetDevice := southboundtest.NewMockTargetIf(ctrl)
	assert.NotNil(t, mockTargetDevice)
	southbound.Targets[id] = mockTargetDevice
	deviceContext, cancel := context.WithCancel(context.Background())
	mockTargetDevice.EXPECT().Context().Return(&deviceContext).AnyTimes()
	return mockTargetDevice, cancel
}

// Make a Network change and it propagates down to 2 Device changes
// They get reconciled successfully
func Test_NewController2Devices(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	networkChanges, deviceChanges, devices := newStores(t, ctrl)
	defer networkChanges.Close()
	defer deviceChanges.Close()

	deviceCache := newDeviceCache(ctrl, device1, device2)
	defer deviceCache.Close()

	leadershipStore, err := leadershipstore.NewLocalStore("cluster-1", "node-1")
	assert.NoError(t, err)
	defer leadershipStore.Close()

	mastershipStore, err := mastershipstore.NewLocalStore("cluster-1", "node-1")
	assert.NoError(t, err)
	defer mastershipStore.Close()

	networkChangeController, deviceChangeController := setupControllers(t, networkChanges, deviceChanges, devices,
		deviceCache, leadershipStore, mastershipStore)

	mockTargetDevice1, cancel1 := newMockTarget(t, ctrl, topodevice.ID(device1))
	defer cancel1()
	mockTargetDevice1.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil, nil).Times(1)

	mockTargetDevice2, cancel2 := newMockTarget(t, ctrl, topodevice.ID(device2))
	defer cancel2()
	mockTargetDevice2.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil, nil).Times(1)

	err = networkChangeController.Start()
	assert.NoError(t, err)
	defer networkChangeController.Stop()

	err = deviceChangeController.Start()
	assert.NoError(t, err)
	defer deviceChangeController.Stop()

	// Create a network change
	networkChange1 := &networkchange.NetworkChange{
		ID: "change-1",
		Changes: []*devicechange.Change{
			&deviceChange1,
			&deviceChange2,
		},
	}

	// Should cause an event to be sent to the Watcher
	// Watcher should pass it to the Reconciler (if not filtered)
	// Reconciler should process it
	// Verify that device changes were created
	err = networkChanges.Create(networkChange1)
	assert.NoError(t, err)

	change1Chan := make(chan stream.Event)
	ctx, err := networkChanges.Watch(change1Chan, networkchangestore.WithReplay())
	assert.NoError(t, err)
	assert.NotNil(t, ctx)

	var wg sync.WaitGroup
	eventsExpected := 4
	wg.Add(eventsExpected) // It takes 4 turns of the reconciler to get it right

	for i := 0; i < eventsExpected; i++ {
		select {
		case event := <-change1Chan:
			change := event.Object.(*networkchange.NetworkChange)
			t.Logf("%s event %d. %v", change.ID, i, change.Status)
			assert.Equal(t, types.Phase_CHANGE, change.Status.Phase)
			assert.Equal(t, types.Reason_NONE, change.Status.Reason)
			switch i {
			case 0, 1:
				assert.Equal(t, types.State_PENDING, change.Status.State)
				assert.Equal(t, uint64(0), change.Status.Incarnation)
			case 2:
				assert.Equal(t, types.State_PENDING, change.Status.State)
				assert.Equal(t, uint64(1), change.Status.Incarnation)
			case 3:
				assert.Equal(t, types.State_COMPLETE, change.Status.State)
				assert.Equal(t, uint64(1), change.Status.Incarnation)
			default:
				t.Errorf("unexpected event on change-1 %v", change)
			}
			wg.Done()
		case <-time.After(500 * time.Millisecond):
			t.Logf("timed out waiting for event from change-1")
			t.FailNow()
		}
	}
	wg.Wait()
	t.Logf("Done waiting for %d change-1 events", eventsExpected)
	ctx.Close()

	networkChange1, err = networkChanges.Get(networkChange1.GetID())
	assert.NoError(t, err)
	assert.Equal(t, types.Phase_CHANGE, networkChange1.Status.Phase)
	assert.Equal(t, types.State_COMPLETE, networkChange1.Status.State)
	assert.Equal(t, uint64(1), networkChange1.Status.Incarnation)

	deviceChange1, err := deviceChanges.Get("change-1:device-1:1.0.0")
	assert.NoError(t, err)
	assert.NotNil(t, deviceChange1)
	assert.Equal(t, types.Phase_CHANGE, deviceChange1.Status.Phase)
	assert.Equal(t, types.State_COMPLETE, deviceChange1.Status.State)
	assert.Equal(t, types.Reason_NONE, deviceChange1.Status.Reason)
	assert.Equal(t, "", deviceChange1.Status.Message)
	assert.Equal(t, uint64(1), deviceChange1.Status.Incarnation)
	deviceChange2, err := deviceChanges.Get("change-1:device-2:1.0.0")
	assert.NoError(t, err)
	assert.NotNil(t, deviceChange2)
	assert.Equal(t, types.Phase_CHANGE, deviceChange2.Status.Phase)
	assert.Equal(t, types.State_COMPLETE, deviceChange2.Status.State)
	assert.Equal(t, types.Reason_NONE, deviceChange2.Status.Reason)
	assert.Equal(t, "", deviceChange2.Status.Message)
	assert.Equal(t, uint64(1), deviceChange2.Status.Incarnation)
}

// a NetworkChange is made to 2 devices. One of the devices returns an error on Set
// and so the NetworkChange is rolled back, rolling back the devicechange on both devices,
// in the end leaving both devices unchanged.
// The Network and Device changes sit there in COMPLETED state in the ROLLBACK phase.
func Test_NewController1FailsGnmiSet(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	networkChanges, deviceChanges, devices := newStores(t, ctrl)
	defer networkChanges.Close()
	defer deviceChanges.Close()

	deviceCache := newDeviceCache(ctrl, device1, device2)
	defer deviceCache.Close()

	leadershipStore, err := leadershipstore.NewLocalStore("cluster-1", "node-1")
	assert.NoError(t, err)
	defer leadershipStore.Close()

	mastershipStore, err := mastershipstore.NewLocalStore("cluster-1", "node-1")
	assert.NoError(t, err)
	defer mastershipStore.Close()

	networkChangeController, deviceChangeController := setupControllers(t, networkChanges, deviceChanges, devices,
		deviceCache, leadershipStore, mastershipStore)

	mockTargetDevice1, cancel1 := newMockTarget(t, ctrl, topodevice.ID(device1))
	defer cancel1()
	mockTargetDevice1.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)

	mockTargetDevice2, cancel2 := newMockTarget(t, ctrl, topodevice.ID(device2))
	defer cancel2()
	// First time will return an error
	mockTargetDevice2.EXPECT().Set(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, request *gnmi.SetRequest) (*gnmi.SetResponse, error) {
			return nil, status.Errorf(codes.Internal, "simulated error in device-2 %s", request)
		}).Times(1)
	// Second time will be a rollback when SET is not possible - no error
	mockTargetDevice2.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil, nil).Times(1)

	err = networkChangeController.Start()
	assert.NoError(t, err)
	defer networkChangeController.Stop()

	err = deviceChangeController.Start()
	assert.NoError(t, err)
	defer deviceChangeController.Stop()

	// Create a network change
	networkChange1 := &networkchange.NetworkChange{
		ID: "change-1",
		Changes: []*devicechange.Change{
			&deviceChange1,
			&deviceChange2,
		},
	}

	// Should cause an event to be sent to the Watcher
	// Watcher should pass it to the Reconciler (if not filtered)
	// Reconciler should process it
	// Verify that device changes were created
	err = networkChanges.Create(networkChange1)
	assert.NoError(t, err)

	change1Chan := make(chan stream.Event)
	ctx, err := networkChanges.Watch(change1Chan, networkchangestore.WithReplay())
	assert.NoError(t, err)
	assert.NotNil(t, ctx)

	device2Chan := make(chan stream.Event)
	ctx, err = deviceChanges.Watch(device.NewVersionedID(device2, v1), device2Chan, devicechangestore.WithReplay())
	assert.NoError(t, err)
	assert.NotNil(t, ctx)

	var wg sync.WaitGroup
	eventsExpectedChange1 := 4
	eventsExpectedDevice2 := 5
	wg.Add(eventsExpectedChange1 + eventsExpectedDevice2) // It can take several turns of the reconciler to complete the change
	var j, k int
	for i := 0; i < eventsExpectedChange1+eventsExpectedDevice2; i++ {
		select {
		case event := <-change1Chan:
			change := event.Object.(*networkchange.NetworkChange)
			t.Logf("%s event %d. %v", change.ID, j, change.Status)
			assert.Equal(t, types.Phase_CHANGE, change.Status.Phase)
			assert.Equal(t, types.State_PENDING, change.Status.State)
			switch j {
			case 0, 1:
				assert.Equal(t, types.Reason_NONE, change.Status.Reason)
				assert.Equal(t, uint64(0), change.Status.Incarnation)
			case 2:
				assert.Equal(t, types.Reason_NONE, change.Status.Reason)
				assert.Equal(t, uint64(1), change.Status.Incarnation)
			case 3:
				assert.Equal(t, types.Reason_ERROR, change.Status.Reason)
				assert.Equal(t, `change rejected by device`,
					strings.ReplaceAll(change.Status.Message, "  ", " "))
				assert.Equal(t, uint64(1), change.Status.Incarnation)
			default:
				t.Errorf("unexpected event on device-1 %v", change)
			}
			j++
			wg.Done()
		case event := <-device2Chan:
			change := event.Object.(*devicechange.DeviceChange)
			t.Logf("%s event %d. %v", change.ID, k, change.Status)
			k++
			wg.Done()
		case <-time.After(500 * time.Millisecond):
			t.Logf("timed out waiting for event from change-1")
			t.FailNow()
		}
	}

	wg.Wait()
	t.Logf("Done waiting for %d change-1 and %d device-2 events", eventsExpectedChange1, eventsExpectedDevice2)
	ctx.Close()

	networkChange1, err = networkChanges.Get(networkChange1.GetID())
	assert.NoError(t, err)
	assert.Equal(t, types.Phase_CHANGE, networkChange1.Status.Phase)
	assert.Equal(t, types.State_PENDING, networkChange1.Status.State)
	assert.Equal(t, types.Reason_ERROR, networkChange1.Status.Reason)
	assert.Equal(t, "change rejected by device", networkChange1.Status.Message)
	assert.Equal(t, uint64(1), networkChange1.Status.Incarnation)

	deviceChange1, err := deviceChanges.Get("change-1:device-1:1.0.0")
	assert.NoError(t, err)
	assert.NotNil(t, deviceChange1)
	assert.Equal(t, types.Phase_ROLLBACK, deviceChange1.Status.Phase)
	assert.Equal(t, types.State_COMPLETE, deviceChange1.Status.State)
	assert.Equal(t, types.Reason_NONE, deviceChange1.Status.Reason)
	assert.Equal(t, "", deviceChange1.Status.Message)
	assert.Equal(t, uint64(1), deviceChange1.Status.Incarnation)
	deviceChange2, err := deviceChanges.Get("change-1:device-2:1.0.0")
	assert.NoError(t, err)
	assert.NotNil(t, deviceChange2)
	assert.Equal(t, types.Phase_ROLLBACK, deviceChange2.Status.Phase)
	assert.Equal(t, types.State_COMPLETE, deviceChange2.Status.State)
	assert.Equal(t, types.Reason_ERROR, deviceChange2.Status.Reason)
	assert.Equal(t, `rpc error: code = Internal desc = simulated error in device-2 update:{path:{elem:{name:"baz"}} val:{string_val:"Goodbye world!"}}`,
		strings.ReplaceAll(deviceChange2.Status.Message, "  ", " "))
	assert.Equal(t, uint64(1), deviceChange2.Status.Incarnation)

	// Should give 5 attempts 20+40+80 ms
	// Can't verify in a test though as different platforms will run at different speeds
	time.Sleep(50 * time.Millisecond)
}

// a NetworkChange is made to 2 devices, which succeeds
// Then rollback the network change, but one of the devices does not accept the rollback
// The Network and Device changes sit there in COMPLETED state in the ROLLBACK phase.
func Test_ControllerDoRollbackWhichFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	networkChanges, deviceChanges, devices := newStores(t, ctrl)
	defer networkChanges.Close()
	defer deviceChanges.Close()

	deviceCache := newDeviceCache(ctrl, device1, device2)
	defer deviceCache.Close()

	leadershipStore, err := leadershipstore.NewLocalStore("cluster-1", "node-1")
	assert.NoError(t, err)
	defer leadershipStore.Close()

	mastershipStore, err := mastershipstore.NewLocalStore("cluster-1", "node-1")
	assert.NoError(t, err)
	defer mastershipStore.Close()

	networkChangeController, deviceChangeController := setupControllers(t, networkChanges, deviceChanges, devices,
		deviceCache, leadershipStore, mastershipStore)

	mockTargetDevice1, cancel1 := newMockTarget(t, ctrl, topodevice.ID(device1))
	defer cancel1()
	mockTargetDevice1.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)

	mockTargetDevice2, cancel2 := newMockTarget(t, ctrl, topodevice.ID(device2))
	defer cancel2()
	// First time is the SET - no error
	mockTargetDevice2.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil, nil).Times(1)
	// Second time will be a rollback but SET returns error
	mockTargetDevice2.EXPECT().Set(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, request *gnmi.SetRequest) (*gnmi.SetResponse, error) {
			return nil, status.Errorf(codes.Internal, "simulated error on rollback in device-2 %s", request)
		}).Times(1)
	// Third time will be a roll forward but here too SET returns error
	mockTargetDevice2.EXPECT().Set(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, request *gnmi.SetRequest) (*gnmi.SetResponse, error) {
			return nil, status.Errorf(codes.Internal, "simulated error on undoing rollback in device-2 %s", request)
		}).Times(1)

	err = networkChangeController.Start()
	assert.NoError(t, err)
	defer networkChangeController.Stop()

	err = deviceChangeController.Start()
	assert.NoError(t, err)
	defer deviceChangeController.Stop()

	// Create a network change
	networkChange1 := &networkchange.NetworkChange{
		ID: "change-1",
		Changes: []*devicechange.Change{
			&deviceChange1,
			&deviceChange2,
		},
	}
	err = networkChanges.Create(networkChange1)
	assert.NoError(t, err)

	device2Chan := make(chan stream.Event)
	ctx, err := deviceChanges.Watch(device.NewVersionedID(device2, v1), device2Chan, devicechangestore.WithReplay())
	assert.NoError(t, err)
	assert.NotNil(t, ctx)

	var wg sync.WaitGroup
	eventsExpected := 3
	wg.Add(eventsExpected) // It can take several turns of the reconciler to complete the change
	for i := 0; i < eventsExpected; i++ {
		select {
		case event := <-device2Chan:
			change := event.Object.(*devicechange.DeviceChange)
			t.Logf("%s event %d. %v", change.ID, i, change.Status)
			switch i {
			case 0:
				assert.Equal(t, types.Phase_CHANGE, change.Status.Phase)
				assert.Equal(t, types.State_PENDING, change.Status.State)
				assert.Equal(t, types.Reason_NONE, change.Status.Reason)
				assert.Equal(t, uint64(0), change.Status.Incarnation)
			case 1:
				assert.Equal(t, types.Phase_CHANGE, change.Status.Phase)
				assert.Equal(t, types.State_PENDING, change.Status.State)
				assert.Equal(t, types.Reason_NONE, change.Status.Reason)
				assert.Equal(t, uint64(1), change.Status.Incarnation)
			case 2:
				assert.Equal(t, types.Phase_CHANGE, change.Status.Phase)
				assert.Equal(t, types.State_COMPLETE, change.Status.State)
				assert.Equal(t, types.Reason_NONE, change.Status.Reason)
				assert.Equal(t, uint64(1), change.Status.Incarnation)
			default:
				t.Errorf("unexpected event on device-2 %v", change)
				t.FailNow()
			}
			wg.Done()
		case <-time.After(500 * time.Millisecond):
			t.Logf("timed out waiting for event from device-2")
			t.FailNow()
		}
	}
	wg.Wait()
	t.Logf("Done waiting for %d device-2 events", eventsExpected)

	retryUpdate := 0
	for { // It can happen that the controller will make another update after the GET
		t.Logf("Trying to do a Rollback %d", retryUpdate)
		changeRollback, errGet := networkChanges.Get(change1)
		assert.NoError(t, errGet)
		if errGet != nil {
			t.FailNow()
		}
		assert.NotNil(t, changeRollback)
		changeRollback.Status.Incarnation++
		changeRollback.Status.Phase = types.Phase_ROLLBACK
		changeRollback.Status.State = types.State_PENDING
		changeRollback.Status.Reason = types.Reason_NONE
		changeRollback.Status.Message = "Administratively requested rollback"

		err = networkChanges.Update(changeRollback)
		// It might fail with "write condition failed" - retry up to 10 times
		if err != nil && err.Error() == "write condition failed" && retryUpdate < 10 {
			time.Sleep(10 * time.Millisecond)
			t.Logf("Retrying update (#%d) after '%s'", retryUpdate, err.Error())
			retryUpdate++
			continue
		}
		break
	}
	assert.NoError(t, err)

	change1Chan := make(chan stream.Event)
	ctx, err = networkChanges.Watch(change1Chan, networkchangestore.WithReplay())
	assert.NoError(t, err)
	assert.NotNil(t, ctx)

	eventsExpectedChange1 := 2
	eventsExpectedDevice2 := 4
	wg.Add(eventsExpectedChange1 + eventsExpectedDevice2) // It can take several turns of the reconciler to complete the change
	var j, k int
	for i := 0; i < eventsExpectedChange1+eventsExpectedDevice2; i++ {
		select {
		case event := <-change1Chan:
			change := event.Object.(*networkchange.NetworkChange)
			t.Logf("%s event %d. %v", change.ID, j, change.Status)
			assert.Equal(t, types.Phase_ROLLBACK, change.Status.Phase)
			assert.Equal(t, types.State_PENDING, change.Status.State)
			switch j {
			case 0:
				assert.Equal(t, types.Reason_NONE, change.Status.Reason)
				assert.Equal(t, `Administratively requested rollback`, change.Status.Message)
				assert.Equal(t, uint64(2), change.Status.Incarnation)
			case 1:
				assert.Equal(t, types.Reason_ERROR, change.Status.Reason)
				assert.Equal(t, `rollback rejected by device`, change.Status.Message)
				assert.Equal(t, uint64(2), change.Status.Incarnation)
			default:
				t.Errorf("unexpected event on device-1 %v", change)
			}
			j++
			wg.Done()
		case event := <-device2Chan:
			change := event.Object.(*devicechange.DeviceChange)
			t.Logf("%s event %d. %v", change.ID, k, change.Status)
			switch k {
			case 0:
				assert.Equal(t, types.Phase_ROLLBACK, change.Status.Phase)
				assert.Equal(t, types.State_PENDING, change.Status.State)
				assert.Equal(t, types.Reason_NONE, change.Status.Reason)
				assert.Equal(t, uint64(2), change.Status.Incarnation)
			case 1:
				assert.Equal(t, types.Phase_ROLLBACK, change.Status.Phase)
				assert.Equal(t, types.State_FAILED, change.Status.State)
				assert.Equal(t, types.Reason_ERROR, change.Status.Reason)
				assert.Equal(t, `rpc error: code = Internal desc = simulated error on rollback in device-2 delete:{elem:{name:"baz"}}`,
					strings.ReplaceAll(change.Status.Message, "  ", " "))
				assert.Equal(t, uint64(2), change.Status.Incarnation)
			case 2:
				assert.Equal(t, types.Phase_CHANGE, change.Status.Phase)
				assert.Equal(t, types.State_PENDING, change.Status.State)
				assert.Equal(t, types.Reason_ERROR, change.Status.Reason)
				assert.Equal(t, `rpc error: code = Internal desc = simulated error on rollback in device-2 delete:{elem:{name:"baz"}}`,
					strings.ReplaceAll(change.Status.Message, "  ", " "))
				assert.Equal(t, uint64(2), change.Status.Incarnation)
			case 3:
				assert.Equal(t, types.Phase_CHANGE, change.Status.Phase)
				assert.Equal(t, types.State_FAILED, change.Status.State)
				assert.Equal(t, types.Reason_ERROR, change.Status.Reason)
				assert.Equal(t, `rpc error: code = Internal desc = simulated error on undoing rollback in device-2 update:{path:{elem:{name:"baz"}} val:{string_val:"Goodbye world!"}}`,
					strings.ReplaceAll(change.Status.Message, "  ", " "))
				assert.Equal(t, uint64(2), change.Status.Incarnation)
			default:
				t.Errorf("unexpected event on device-2 %v", change)
				t.FailNow()
			}
			k++
			wg.Done()
		case <-time.After(500 * time.Millisecond):
			t.Logf("timed out waiting for event from device-2")
			t.FailNow()
		}
	}
	wg.Wait()
	t.Logf("Done waiting for %d change-1 and %d device-2 events", eventsExpectedChange1, eventsExpectedDevice2)
	ctx.Close()

	networkChange1, err = networkChanges.Get(networkChange1.GetID())
	assert.NoError(t, err)
	assert.Equal(t, types.Phase_ROLLBACK, networkChange1.Status.Phase)
	assert.Equal(t, types.State_PENDING, networkChange1.Status.State)
	assert.Equal(t, types.Reason_ERROR, networkChange1.Status.Reason)
	assert.Equal(t, "rollback rejected by device", networkChange1.Status.Message)
	assert.Equal(t, uint64(2), networkChange1.Status.Incarnation)

	deviceChange1, err := deviceChanges.Get("change-1:device-1:1.0.0")
	assert.NoError(t, err)
	assert.NotNil(t, deviceChange1)
	assert.Equal(t, types.Phase_ROLLBACK, deviceChange1.Status.Phase)
	assert.Equal(t, types.State_COMPLETE, deviceChange1.Status.State)
	assert.Equal(t, types.Reason_NONE, deviceChange1.Status.Reason)
	assert.Equal(t, "", deviceChange1.Status.Message)
	assert.Equal(t, uint64(2), deviceChange1.Status.Incarnation)
	deviceChange2, err := deviceChanges.Get("change-1:device-2:1.0.0")
	assert.NoError(t, err)
	assert.NotNil(t, deviceChange2)
	assert.Equal(t, types.Phase_CHANGE, deviceChange2.Status.Phase)
	assert.Equal(t, types.State_FAILED, deviceChange2.Status.State)
	assert.Equal(t, types.Reason_ERROR, deviceChange2.Status.Reason)
	assert.Equal(t, `rpc error: code = Internal desc = simulated error on undoing rollback in device-2 update:{path:{elem:{name:"baz"}} val:{string_val:"Goodbye world!"}}`,
		strings.ReplaceAll(deviceChange2.Status.Message, "  ", " "))
	assert.Equal(t, uint64(2), deviceChange2.Status.Incarnation)

	// Should give 5 attempts 20+40+80 ms
	// Can't verify in a test though as different platforms will run at different speeds
	time.Sleep(50 * time.Millisecond)
}

// A Network Change is made to 2 devices.
// But neither of the devices are connected - not in the device cache
// We want to rollback this NetworkChange, rolling back the devicechange on both devices,
// in the end leaving both devices unchanged.
// The Network and Device changes sit there in COMPLETED state in the ROLLBACK phase.
func Test_ControllerRollbackOnPending(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	networkChanges, deviceChanges, devices := newStores(t, ctrl)
	defer networkChanges.Close()
	defer deviceChanges.Close()

	deviceCache := newDeviceCache(ctrl)
	defer deviceCache.Close()

	leadershipStore, err := leadershipstore.NewLocalStore("cluster-1", "node-1")
	assert.NoError(t, err)
	defer leadershipStore.Close()

	mastershipStore, err := mastershipstore.NewLocalStore("cluster-1", "node-1")
	assert.NoError(t, err)
	defer mastershipStore.Close()

	networkChangeController, deviceChangeController := setupControllers(t, networkChanges, deviceChanges, devices,
		deviceCache, leadershipStore, mastershipStore)

	err = networkChangeController.Start()
	assert.NoError(t, err)
	defer networkChangeController.Stop()

	err = deviceChangeController.Start()
	assert.NoError(t, err)
	defer deviceChangeController.Stop()

	// Create a network change
	networkChange1 := &networkchange.NetworkChange{
		ID: "change-1",
		Changes: []*devicechange.Change{
			&deviceChange1,
			&deviceChange2,
		},
	}

	err = networkChanges.Create(networkChange1)
	assert.NoError(t, err)

	change1Chan := make(chan stream.Event)
	ctx, err := networkChanges.Watch(change1Chan, networkchangestore.WithReplay())
	assert.NoError(t, err)
	assert.NotNil(t, ctx)

	device2Chan := make(chan stream.Event)
	ctx, err = deviceChanges.Watch(device.NewVersionedID(device2, v1), device2Chan, devicechangestore.WithReplay())
	assert.NoError(t, err)
	assert.NotNil(t, ctx)

	var wg sync.WaitGroup
	eventsExpectedChange1 := 3
	eventsExpectedDevice2 := 2
	wg.Add(eventsExpectedChange1 + eventsExpectedDevice2) // It can take several turns of the reconciler to complete the change
	var j, k int
	for i := 0; i < eventsExpectedChange1+eventsExpectedDevice2; i++ {
		select {
		case event := <-change1Chan:
			change := event.Object.(*networkchange.NetworkChange)
			t.Logf("%s event %d. %v", change.ID, j, change.Status)
			assert.Equal(t, types.Phase_CHANGE, change.Status.Phase)
			assert.Equal(t, types.State_PENDING, change.Status.State)
			assert.Equal(t, types.Reason_NONE, change.Status.Reason)
			switch j {
			case 0, 1:
				assert.Equal(t, uint64(0), change.Status.Incarnation)
			case 2:
				assert.Equal(t, uint64(1), change.Status.Incarnation)
			default:
				t.Errorf("unexpected event on change-1 %v", change)
			}
			j++
			wg.Done()
		case event := <-device2Chan:
			change := event.Object.(*devicechange.DeviceChange)
			t.Logf("%s event %d. %v", change.ID, k, change.Status)
			assert.Equal(t, types.Phase_CHANGE, change.Status.Phase)
			assert.Equal(t, types.State_PENDING, change.Status.State)
			assert.Equal(t, types.Reason_NONE, change.Status.Reason)
			switch k {
			case 0:
				assert.Equal(t, uint64(0), change.Status.Incarnation)
			case 1:
				assert.Equal(t, uint64(1), change.Status.Incarnation)
			default:
				t.Errorf("unexpected event on device-2 %v", change)
				t.FailNow()
			}
			k++
			wg.Done()
		case <-time.After(500 * time.Millisecond):
			t.Logf("timed out waiting for event from change-1 and device-2")
			t.FailNow()
		}
	}

	wg.Wait()
	t.Logf("Done waiting for %d change-1 and %d device-2 events", eventsExpectedChange1, eventsExpectedDevice2)

	// Now try to do a rollback of these changes after a moment
	// Should cause an event to be sent to the Watcher
	// Watcher should pass it to the Reconciler (if not filtered)
	// Reconciler should process it
	time.Sleep(50 * time.Millisecond)
	retryUpdate := 0
	for { // It can happen that the controller will make another update after the GET
		t.Logf("Trying to do a Rollback %d", retryUpdate)
		changeRollback, errGet := networkChanges.Get(change1)
		assert.NoError(t, errGet)
		if errGet != nil {
			t.FailNow()
		}
		assert.NotNil(t, changeRollback)
		changeRollback.Status.Incarnation++
		changeRollback.Status.Phase = types.Phase_ROLLBACK
		changeRollback.Status.State = types.State_PENDING
		changeRollback.Status.Reason = types.Reason_NONE
		changeRollback.Status.Message = "Administratively requested rollback"

		err = networkChanges.Update(changeRollback)
		// It might fail with "write condition failed" - retry up to 10 times
		if err != nil && err.Error() == "write condition failed" && retryUpdate < 10 {
			time.Sleep(10 * time.Millisecond)
			t.Logf("Retrying update (#%d) after '%s'", retryUpdate, err.Error())
			retryUpdate++
			continue
		}
		break
	}
	assert.NoError(t, err)

	eventsExpectedChange1 = 2
	eventsExpectedDevice2 = 1
	wg.Add(eventsExpectedChange1 + eventsExpectedDevice2) // It can take several turns of the reconciler to complete the change
	j = 0
	k = 0
	for i := 0; i < eventsExpectedChange1+eventsExpectedDevice2; i++ {
		select {
		case event := <-change1Chan:
			change := event.Object.(*networkchange.NetworkChange)
			t.Logf("%s event %d. %v", change.ID, j, change.Status)
			assert.Equal(t, types.Phase_ROLLBACK, change.Status.Phase)
			assert.Equal(t, types.Reason_NONE, change.Status.Reason)
			assert.Equal(t, `Administratively requested rollback`, change.Status.Message)
			assert.Equal(t, uint64(2), change.Status.Incarnation)
			switch j {
			case 0:
				assert.Equal(t, types.State_PENDING, change.Status.State)
			case 1:
				assert.Equal(t, types.State_COMPLETE, change.Status.State)
			default:
				t.Errorf("unexpected event on change-1 %v", change)
			}
			j++
			wg.Done()
		case event := <-device2Chan:
			change := event.Object.(*devicechange.DeviceChange)
			t.Logf("%s event %d. %v", change.ID, k, change.Status)
			switch k {
			case 0:
				assert.Equal(t, types.Phase_ROLLBACK, change.Status.Phase)
				assert.Equal(t, types.State_COMPLETE, change.Status.State)
				assert.Equal(t, types.Reason_NONE, change.Status.Reason)
				assert.Equal(t, uint64(2), change.Status.Incarnation)
			default:
				t.Errorf("unexpected event on device-2 %v", change)
				t.FailNow()
			}
			k++
			wg.Done()
		case <-time.After(500 * time.Millisecond):
			t.Logf("timed out waiting for event from change-1 after rollback")
			t.FailNow()
		}
	}

	wg.Wait()
	t.Logf("Done waiting for %d change-1 and %d events", eventsExpectedChange1, eventsExpectedDevice2)
	ctx.Close()

	// Verify that device changes were created
	networkChange1, err = networkChanges.Get(networkChange1.GetID())
	assert.NoError(t, err)
	assert.Equal(t, types.Phase_ROLLBACK, networkChange1.Status.Phase)
	assert.Equal(t, types.State_COMPLETE, networkChange1.Status.State)
	assert.Equal(t, types.Reason_NONE, networkChange1.Status.Reason)
	assert.Equal(t, "Administratively requested rollback", networkChange1.Status.Message)
	assert.Equal(t, uint64(2), networkChange1.Status.Incarnation)

	deviceChange1, err := deviceChanges.Get("change-1:device-1:1.0.0")
	assert.NoError(t, err)
	assert.NotNil(t, deviceChange1)
	assert.Equal(t, types.Phase_ROLLBACK, deviceChange1.Status.Phase)
	assert.Equal(t, types.State_COMPLETE, deviceChange1.Status.State)
	assert.Equal(t, types.Reason_NONE, deviceChange1.Status.Reason)
	assert.Equal(t, "", deviceChange1.Status.Message)
	assert.Equal(t, uint64(2), deviceChange1.Status.Incarnation)
	deviceChange2, err := deviceChanges.Get("change-1:device-2:1.0.0")
	assert.NoError(t, err)
	assert.NotNil(t, deviceChange2)
	assert.Equal(t, types.Phase_ROLLBACK, deviceChange2.Status.Phase)
	assert.Equal(t, types.State_COMPLETE, deviceChange2.Status.State)
	assert.Equal(t, types.Reason_NONE, deviceChange2.Status.Reason)
	assert.Equal(t, "", deviceChange2.Status.Message)
	assert.Equal(t, uint64(2), deviceChange2.Status.Incarnation)

	// Should not repeat indefinitely
	time.Sleep(50 * time.Millisecond)
}
