package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/Cicyer/micro/example/proto/micro-service/TestService"
	"github.com/Cicyer/micro/micro"
	"github.com/nacos-group/nacos-sdk-go/common/constant"
	"google.golang.org/grpc"
	"time"
)

type OrderServiceImpl struct {
	TestService.UnimplementedOrderServiceServer
}
type OrderService2Impl struct {
	TestService.UnimplementedOrder2ServiceServer
}

func (c *OrderServiceImpl) GetOrderInfo(ctx context.Context, request *TestService.OrderRequest) (*TestService.OrderInfo, error) {
	orderMap := map[string]TestService.OrderInfo{
		"201907300001": TestService.OrderInfo{OrderId: "201907300001", OrderName: "衣服", OrderStatus: "已付款"},
		"201907310001": TestService.OrderInfo{OrderId: "201907310001", OrderName: "零食", OrderStatus: "已付款"},
		"201907310002": TestService.OrderInfo{OrderId: "201907310002", OrderName: "食品", OrderStatus: "未付款"},
	}
	time.Sleep(time.Second * 2)
	var response *TestService.OrderInfo
	current := time.Now().Unix()
	if request.TimeStamp > current {
		*response = TestService.OrderInfo{OrderId: "0", OrderName: "", OrderStatus: "订单信息异常"}
	} else {
		result := orderMap[request.OrderId]
		if result.OrderId != "" {
			fmt.Println(result)
			return &result, nil
		} else {
			return nil, errors.New("server error")
		}
	}
	return response, nil
}

// 具体的方法实现
func (this *OrderService2Impl) GetOrderInfo(ctx context.Context, request *TestService.OrderRequest2) (*TestService.OrderInfo2, error) {
	orderMap := map[string]TestService.OrderInfo2{
		"201907300001": TestService.OrderInfo2{OrderId: "201907300001", OrderName: "衣服2", OrderStatus: "已付款"},
		"201907310001": TestService.OrderInfo2{OrderId: "201907310001", OrderName: "零食2", OrderStatus: "已付款"},
		"201907310002": TestService.OrderInfo2{OrderId: "201907310002", OrderName: "食品2", OrderStatus: "未付款"},
	}

	var response *TestService.OrderInfo2
	current := time.Now().Unix()
	if request.TimeStamp > current {
		*response = TestService.OrderInfo2{OrderId: "0", OrderName: "", OrderStatus: "订单信息异常"}
	} else {
		result := orderMap[request.OrderId]
		if result.OrderId != "" {
			fmt.Println(result)
			return &result, nil
		} else {
			return nil, errors.New("server error")
		}
	}
	return response, nil
}

func main() {
	clientConfig := constant.ClientConfig{
		NamespaceId:         "e525eafa-f7d7-4029-83d9-008937f9d468", //we can create multiple clients with different namespaceId to support multiple namespace
		TimeoutMs:           5000,
		NotLoadCacheAtStart: true,
		RotateTime:          "1h",
		MaxAge:              3,
		LogLevel:            "debug",
	}
	serverConfigs := []constant.ServerConfig{
		{
			IpAddr:      "localhost",
			ContextPath: "/nacos",
			Port:        8848,
			Scheme:      "http",
		},
	}
	// Create naming client for service discovery
	//namingClient, err := clients.CreateNamingClient(map[string]interface{}{
	//	"serverConfigs": serverConfigs,
	//	"clientConfig":  clientConfig,
	//})
	//success, err := namingClient.RegisterInstance(vo.RegisterInstanceParam{
	//	Ip:          "127.0.0.1",
	//	Port:        8090,
	//	ServiceName: "TestService",
	//	Weight:      10,
	//	Enable:      true,
	//	Healthy:     true,
	//	Ephemeral:   true,
	//	Metadata:    map[string]string{"idc": "shanghai"},
	//	ClusterName: "cluster-a", // default value is DEFAULT
	//	GroupName:   "group-a",   // default value is DEFAULT_GROUP
	//})
	//if success {
	//	lis, err := net.Listen("tcp", ":8090")
	//	if err != nil {
	//		panic(err.Error())
	//	}
	//	server := grpc.NewServer()
	//	TestService.RegisterOrderServiceServer(server, new(OrderServiceImpl))
	//	TestService.RegisterOrder2ServiceServer(server, new(OrderService2Impl))
	//	server.Serve(lis)
	//} else {
	//	panic(err.Error())
	//}
	//
	//provider, err := micro.CreateNacosProvider(RegisterTestService, &clientConfig, &serverConfigs, "TestService", "127.0.0.1", "cluster-default", "group-default", &map[string]string{"idc": "shanghai"})
	provider, err := micro.CreateNacosCircuitProvider(RegisterTestService, &clientConfig, &serverConfigs, "TestService", "127.0.0.1", "cluster-default", "group-default", &map[string]string{"idc": "shanghai"},
		micro.CircuitConfig{
			TimeoutThresholdRate: 0.5,
			TimeoutDuration:      time.Second * 1,
			RetryDuration:        time.Second * 10,
			CircuitDuration:      time.Second * 15,
			CircuitBaseCount:     5,
		})
	if err != nil {
		panic(err.Error())
	}
	err = micro.StartProvide(provider.GetBaseProvider(), "8092")
	if err != nil {
		panic(err.Error())
	}
}
func RegisterTestService(s *grpc.Server) {
	TestService.RegisterOrderServiceServer(s, new(OrderServiceImpl))
	TestService.RegisterOrder2ServiceServer(s, new(OrderService2Impl))
}
