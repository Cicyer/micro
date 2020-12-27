package micro

import (
	"errors"
	"fmt"
	"github.com/nacos-group/nacos-sdk-go/clients"
	"github.com/nacos-group/nacos-sdk-go/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/common/constant"
	"github.com/nacos-group/nacos-sdk-go/model"
	"github.com/nacos-group/nacos-sdk-go/vo"
	"google.golang.org/grpc"
	"strconv"
	"sync/atomic"
	"time"
)

type NacosProvider struct {
	clientConfig  *constant.ClientConfig
	serverConfigs *[]constant.ServerConfig
	namingClient  *naming_client.INamingClient
	registerFunc  GrpcRegisterFunc
	serviceName   string
	port          uint64
	ip            string
	metadata      *map[string]string
	clusterName   string
	groupName     string
}
type NacosConsumer struct {
	clientConfig  *constant.ClientConfig
	serverConfigs *[]constant.ServerConfig
	namingClient  *naming_client.INamingClient
	serviceName   string
	conn          *grpc.ClientConn
	clusterName   string
	groupName     string
	process       int64
	needRelink    bool
	subscribed    bool
}

func CreateNacosProvider(foo GrpcRegisterFunc, clientConfig *constant.ClientConfig, serverConfigs *[]constant.ServerConfig, ServiceName string) (provider *NacosProvider, err error) {
	provider = &NacosProvider{}
	namingClient, err := clients.CreateNamingClient(map[string]interface{}{
		"serverConfigs": *serverConfigs,
		"clientConfig":  *clientConfig,
	})
	if err == nil {
		provider.registerFunc = foo
		provider.clientConfig = clientConfig
		provider.serverConfigs = serverConfigs
		provider.namingClient = &namingClient
		provider.serviceName = ServiceName
		provider.clusterName = "cluster-default"
		provider.groupName = "group-default"
		provider.ip = "127.0.0.1"
		provider.metadata = &map[string]string{"idc": "shanghai"}
	}
	return
}

func CreateNacosConsumer(clientConfig *constant.ClientConfig, serverConfigs *[]constant.ServerConfig, ServiceName string) (consumer *NacosConsumer, err error) {
	consumer = &NacosConsumer{}
	// Create naming client for service discovery
	namingClient, err := clients.CreateNamingClient(map[string]interface{}{
		"serverConfigs": *serverConfigs,
		"clientConfig":  *clientConfig,
	})
	if err != nil {
		return
	}
	if err == nil {
		consumer.clientConfig = clientConfig
		consumer.serverConfigs = serverConfigs
		consumer.namingClient = &namingClient
		consumer.serviceName = ServiceName
		consumer.needRelink = false
		consumer.subscribed = false
		consumer.clusterName = "cluster-default"
		consumer.groupName = "group-default"
	}
	return
}

//使用provider的namingClient构建consumer
func (np *NacosProvider) CreateNacosConsumer(ServiceName string) (consumer *NacosConsumer) {
	consumer = &NacosConsumer{}
	consumer.clientConfig = np.clientConfig
	consumer.serverConfigs = np.serverConfigs
	consumer.namingClient = np.namingClient
	consumer.serviceName = ServiceName
	consumer.needRelink = false
	consumer.subscribed = false
	consumer.clusterName = np.clusterName
	consumer.groupName = np.groupName
	return
}

//使用consumer的namingClient构建provider
func (nc *NacosConsumer) CreateNacosProvider(foo GrpcRegisterFunc, ServiceName string) (provider *NacosProvider, err error) {
	provider = &NacosProvider{}
	provider.registerFunc = foo
	provider.clientConfig = nc.clientConfig
	provider.serverConfigs = nc.serverConfigs
	provider.namingClient = nc.namingClient
	provider.serviceName = ServiceName
	provider.clusterName = nc.clusterName
	provider.groupName = nc.groupName
	provider.ip = "127.0.0.1"
	provider.metadata = &map[string]string{"idc": "shanghai"}
	return
}
func (np *NacosProvider) GetServiceName() (serviceName string) {
	return np.serviceName
}
func (np *NacosProvider) RegisterServices(s *grpc.Server, port string) (err error) {
	portInt, err := strconv.Atoi(port)
	if err != nil {
		return
	}
	portUInt := uint64(portInt)
	if np.registerFunc == nil {
		err = errors.New("not found grpc service register")
	} else {
		np.registerFunc(s)
		_, err = (*np.namingClient).RegisterInstance(vo.RegisterInstanceParam{
			Ip:          np.ip,
			Port:        portUInt,
			ServiceName: np.serviceName,
			Weight:      10,
			Enable:      true,
			Healthy:     true,
			Ephemeral:   true,
			Metadata:    *np.metadata,
			ClusterName: np.clusterName, // default value is DEFAULT
			GroupName:   np.groupName,   // default value is DEFAULT_GROUP
		})
		np.port = portUInt
	}
	return
}
func (np *NacosProvider) DeregisterServices() (err error) {
	_, err = (*np.namingClient).DeregisterInstance(vo.DeregisterInstanceParam{
		Ip:          np.ip,
		Port:        np.port,
		ServiceName: np.serviceName,
		Ephemeral:   true,
		Cluster:     np.clusterName, // default value is DEFAULT
		GroupName:   np.groupName,   // default value is DEFAULT_GROUP
	})
	return
}

func (nc *NacosConsumer) GetServiceConnection(serviceName string) (conn *grpc.ClientConn, err error) {
	var tmpConn *grpc.ClientConn
	if nc.conn != nil && (nc.conn.GetState().String() != "SHUTDOWN" && nc.conn.GetState().String() != "Invalid-State") {
		//可用的链接，直接返回
		conn = nc.conn
	} else if nc.conn != nil {
		//链接存在问题。此时断开连接，重新刷新
		err1 := nc.conn.Close()
		if err1 != nil {
			err = errors.New("reset connection fail:" + err1.Error())
			return
		}
		//删除原有conn
		nc.conn = nil
	} else if nc.needRelink {
		nc.needRelink = false
		//如果监听器告知需要重新，则取出当前的conn，防止后续请求继续使用此link
		tmpConn = nc.conn
		nc.conn = nil
	}

	//此时说明要重新获取链接
	if nc.conn == nil {
		err = nc.getServiceConn()
		conn = nc.conn
	}
	if tmpConn != nil {
		//如果有重试
		tmpConn.Close()
	}
	return
}

func (nc *NacosConsumer) GetServiceName() (serviceName string, err error) {
	serviceName = nc.serviceName
	return
}
func (nc *NacosConsumer) Stop() (err error) {
	//取消订阅
	err = (*nc.namingClient).Unsubscribe(&vo.SubscribeParam{
		ServiceName: nc.serviceName,
		GroupName:   nc.groupName,             // 默认值DEFAULT_GROUP
		Clusters:    []string{nc.clusterName}, // 默认值DEFAULT
		SubscribeCallback: func(services []model.SubscribeService, err2 error) {
			fmt.Printf("Unsubscribe services:", err2.Error())
		},
	})
	return
}
func (nc *NacosConsumer) getServiceConn() error {
	current := atomic.AddInt64(&nc.process, 1)
	if current == 1 {
		instance, err1 := (*nc.namingClient).SelectOneHealthyInstance(vo.SelectOneHealthInstanceParam{
			ServiceName: nc.serviceName,
			GroupName:   nc.groupName,             // 默认值DEFAULT_GROUP
			Clusters:    []string{nc.clusterName}, // 默认值DEFAULT
		})
		if err1 != nil {
			atomic.AddInt64(&nc.process, -1)
			return errors.New("reset connection fail:" + err1.Error())
		}
		conn, err1 := grpc.Dial(instance.Ip+":"+strconv.FormatUint(instance.Port, 10), grpc.WithInsecure())
		if err1 != nil {
			atomic.AddInt64(&nc.process, -1)
			return errors.New("reset connection fail:" + err1.Error())
		}
		if !nc.subscribed {
			//订阅，每个服务只会订阅一次
			err := (*nc.namingClient).Subscribe(&vo.SubscribeParam{
				ServiceName: nc.serviceName,
				GroupName:   nc.groupName,             // 默认值DEFAULT_GROUP
				Clusters:    []string{nc.clusterName}, // 默认值DEFAULT
				SubscribeCallback: func(services []model.SubscribeService, err error) {
					fmt.Printf("Subscribe callback return services", err)
					//服务有变化重新创建链接
					nc.needRelink = true
				},
			})
			if err != nil {
				//订阅状态失败
				atomic.AddInt64(&nc.process, -1)
				conn.Close()
				return errors.New("Subscribe services fail:" + err.Error())
			}
			nc.subscribed = true
		}
		//完全成功则赋值
		nc.conn = conn
	} else {
		//等待5s看链接是否重新创建
		time.Sleep(time.Second * 5)
		if nc.conn != nil {
			atomic.AddInt64(&nc.process, -1)
			return nil
		}
		//创建失败
		atomic.AddInt64(&nc.process, -1)
		return errors.New("micro service temporarily not respond")
	}
	atomic.AddInt64(&nc.process, -1)
	return nil
}
