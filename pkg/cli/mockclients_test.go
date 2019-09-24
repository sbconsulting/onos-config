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

// Client Mocks
package cli

import (
	"context"
	"github.com/onosproject/onos-config/pkg/northbound/admin"
	"google.golang.org/grpc"
)

type mockConfigAdminServiceClient struct {
	rollBackID string
}

var LastCreatedClient *mockConfigAdminServiceClient

func (c mockConfigAdminServiceClient) RegisterModel(ctx context.Context, in *admin.RegisterRequest, opts ...grpc.CallOption) (*admin.RegisterResponse, error) {
	return nil, nil
}

func (c mockConfigAdminServiceClient) UploadRegisterModel(ctx context.Context, opts ...grpc.CallOption) (admin.ConfigAdminService_UploadRegisterModelClient, error) {
	return nil, nil
}

func (c mockConfigAdminServiceClient) ListRegisteredModels(ctx context.Context, in *admin.ListModelsRequest, opts ...grpc.CallOption) (admin.ConfigAdminService_ListRegisteredModelsClient, error) {
	return nil, nil
}

func (c mockConfigAdminServiceClient) GetNetworkChanges(ctx context.Context, in *admin.NetworkChangesRequest, opts ...grpc.CallOption) (admin.ConfigAdminService_GetNetworkChangesClient, error) {
	return nil, nil
}

func (c mockConfigAdminServiceClient) RollbackNetworkChange(ctx context.Context, in *admin.RollbackRequest, opts ...grpc.CallOption) (*admin.RollbackResponse, error) {
	response := &admin.RollbackResponse{
		Message:              "Rollback was successful",
		XXX_NoUnkeyedLiteral: struct{}{},
		XXX_unrecognized:     nil,
		XXX_sizecache:        0,
	}
	LastCreatedClient.rollBackID = in.Name
	return response, nil
}

func setUpMockClients() {
	admin.ConfigAdminClientFactory = func(cc *grpc.ClientConn) admin.ConfigAdminServiceClient {
		LastCreatedClient = &mockConfigAdminServiceClient{
			rollBackID: "",
		}
		return LastCreatedClient
	}
}