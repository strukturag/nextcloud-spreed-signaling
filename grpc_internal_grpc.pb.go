//*
// Standalone signaling server for the Nextcloud Spreed app.
// Copyright (C) 2022 struktur AG
//
// @author Joachim Bauch <bauch@struktur.de>
//
// @license GNU AGPL version 3 or any later version
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// source: grpc_internal.proto

package signaling

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.64.0 or later.
const _ = grpc.SupportPackageIsVersion9

const (
	RpcInternal_GetServerId_FullMethodName = "/signaling.RpcInternal/GetServerId"
)

// RpcInternalClient is the client API for RpcInternal service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type RpcInternalClient interface {
	GetServerId(ctx context.Context, in *GetServerIdRequest, opts ...grpc.CallOption) (*GetServerIdReply, error)
}

type rpcInternalClient struct {
	cc grpc.ClientConnInterface
}

func NewRpcInternalClient(cc grpc.ClientConnInterface) RpcInternalClient {
	return &rpcInternalClient{cc}
}

func (c *rpcInternalClient) GetServerId(ctx context.Context, in *GetServerIdRequest, opts ...grpc.CallOption) (*GetServerIdReply, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetServerIdReply)
	err := c.cc.Invoke(ctx, RpcInternal_GetServerId_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RpcInternalServer is the server API for RpcInternal service.
// All implementations must embed UnimplementedRpcInternalServer
// for forward compatibility.
type RpcInternalServer interface {
	GetServerId(context.Context, *GetServerIdRequest) (*GetServerIdReply, error)
	mustEmbedUnimplementedRpcInternalServer()
}

// UnimplementedRpcInternalServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedRpcInternalServer struct{}

func (UnimplementedRpcInternalServer) GetServerId(context.Context, *GetServerIdRequest) (*GetServerIdReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetServerId not implemented")
}
func (UnimplementedRpcInternalServer) mustEmbedUnimplementedRpcInternalServer() {}
func (UnimplementedRpcInternalServer) testEmbeddedByValue()                     {}

// UnsafeRpcInternalServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to RpcInternalServer will
// result in compilation errors.
type UnsafeRpcInternalServer interface {
	mustEmbedUnimplementedRpcInternalServer()
}

func RegisterRpcInternalServer(s grpc.ServiceRegistrar, srv RpcInternalServer) {
	// If the following call pancis, it indicates UnimplementedRpcInternalServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&RpcInternal_ServiceDesc, srv)
}

func _RpcInternal_GetServerId_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetServerIdRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RpcInternalServer).GetServerId(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: RpcInternal_GetServerId_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RpcInternalServer).GetServerId(ctx, req.(*GetServerIdRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// RpcInternal_ServiceDesc is the grpc.ServiceDesc for RpcInternal service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var RpcInternal_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "signaling.RpcInternal",
	HandlerType: (*RpcInternalServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetServerId",
			Handler:    _RpcInternal_GetServerId_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "grpc_internal.proto",
}
