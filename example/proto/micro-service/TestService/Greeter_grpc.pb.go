// Code generated by protoc-gen-go-grpc. DO NOT EDIT.

package TestService

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion7

// Order2ServiceClient is the client API for Order2Service service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type Order2ServiceClient interface {
	GetOrderInfo(ctx context.Context, in *OrderRequest2, opts ...grpc.CallOption) (*OrderInfo2, error)
}

type order2ServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewOrder2ServiceClient(cc grpc.ClientConnInterface) Order2ServiceClient {
	return &order2ServiceClient{cc}
}

func (c *order2ServiceClient) GetOrderInfo(ctx context.Context, in *OrderRequest2, opts ...grpc.CallOption) (*OrderInfo2, error) {
	out := new(OrderInfo2)
	err := c.cc.Invoke(ctx, "/TestService.Order2Service/GetOrderInfo", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Order2ServiceServer is the server API for Order2Service service.
// All implementations must embed UnimplementedOrder2ServiceServer
// for forward compatibility
type Order2ServiceServer interface {
	GetOrderInfo(context.Context, *OrderRequest2) (*OrderInfo2, error)
	mustEmbedUnimplementedOrder2ServiceServer()
}

// UnimplementedOrder2ServiceServer must be embedded to have forward compatible implementations.
type UnimplementedOrder2ServiceServer struct {
}

func (UnimplementedOrder2ServiceServer) GetOrderInfo(context.Context, *OrderRequest2) (*OrderInfo2, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetOrderInfo not implemented")
}
func (UnimplementedOrder2ServiceServer) mustEmbedUnimplementedOrder2ServiceServer() {}

// UnsafeOrder2ServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to Order2ServiceServer will
// result in compilation errors.
type UnsafeOrder2ServiceServer interface {
	mustEmbedUnimplementedOrder2ServiceServer()
}

func RegisterOrder2ServiceServer(s grpc.ServiceRegistrar, srv Order2ServiceServer) {
	s.RegisterService(&Order2Service_ServiceDesc, srv)
}

func _Order2Service_GetOrderInfo_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(OrderRequest2)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(Order2ServiceServer).GetOrderInfo(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/TestService.Order2Service/GetOrderInfo",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(Order2ServiceServer).GetOrderInfo(ctx, req.(*OrderRequest2))
	}
	return interceptor(ctx, in, info, handler)
}

// Order2Service_ServiceDesc is the grpc.ServiceDesc for Order2Service service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Order2Service_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "TestService.Order2Service",
	HandlerType: (*Order2ServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetOrderInfo",
			Handler:    _Order2Service_GetOrderInfo_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "Greeter.proto",
}
