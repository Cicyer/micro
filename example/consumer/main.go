package main

import (
	"context"
	"fmt"
	"github.com/Cicyer/micro/example/proto/micro-service/TestService"
	"github.com/Cicyer/micro/micro"
	"github.com/nacos-group/nacos-sdk-go/common/constant"
	"time"
)

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
	//_, err := clients.CreateNamingClient(map[string]interface{}{
	//	"serverConfigs": serverConfigs,
	//	"clientConfig":  clientConfig,
	//})
	//instance, err := namingClient.SelectOneHealthyInstance(vo.SelectOneHealthInstanceParam{
	//	ServiceName: "TestService",
	//	GroupName:   "group-default",             // 默认值DEFAULT_GROUP
	//	Clusters:    []string{"cluster-default"}, // 默认值DEFAULT
	//})
	//if err != nil {
	//	panic(err.Error())
	//}
	consumer, err := micro.CreateNacosConsumer(&clientConfig, &serverConfigs, "TestService")
	micro.AddConsumer(consumer)
	//
	//conn, err := grpc.Dial(instance.Ip+":"+strconv.FormatUint(instance.Port, 10), grpc.WithInsecure())
	//if err != nil {
	//	panic(err.Error())
	//}
	//defer conn.Close()
	defer micro.Stop()
	conn, err := micro.GetServiceConn("TestService")
	orderServiceClient := TestService.NewOrderServiceClient(conn)
	orderRequest := &TestService.OrderRequest{OrderId: "201907300001", TimeStamp: time.Now().Unix()}
	orderInfo, err := orderServiceClient.GetOrderInfo(context.Background(), orderRequest)
	if err == nil {
		fmt.Println(orderInfo.GetOrderId())
		fmt.Println(orderInfo.GetOrderName())
		fmt.Println(orderInfo.GetOrderStatus())
	}
	//复用测试
	orderService2Client := TestService.NewOrder2ServiceClient(conn)
	orderRequest2 := &TestService.OrderRequest2{OrderId: "201907300001", TimeStamp: time.Now().Unix()}
	orderInfo2, err := orderService2Client.GetOrderInfo(context.Background(), orderRequest2)
	if err == nil {
		fmt.Println(orderInfo2.GetOrderId())
		fmt.Println(orderInfo2.GetOrderName())
		fmt.Println(orderInfo2.GetOrderStatus())
	}

}
