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

type ServiceConnection struct {
	instance   *grpc.ClientConn
	processBtn int64
}
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
	//链接列表，0为主链接，主线路不可用时，业务返回一个备用线路，备用线路会定期关闭回收
	connList       [10]*ServiceConnection
	listBtn        int64
	clusterName    string
	groupName      string
	subscribed     bool
	subscribeCount int64
}

func CreateNacosProvider(foo GrpcRegisterFunc, clientConfig *constant.ClientConfig, serverConfigs *[]constant.ServerConfig, ServiceName string, serviceIp string, clusterName string, groupName string, metadata *map[string]string) (provider *NacosProvider, err error) {
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
		provider.clusterName = clusterName //"cluster-default"
		provider.groupName = groupName     //"group-default"
		provider.ip = serviceIp            //"127.0.0.1"
		provider.metadata = metadata       //&map[string]string{"idc": "shanghai"}
	}
	return
}

func CreateNacosConsumer(clientConfig *constant.ClientConfig, serverConfigs *[]constant.ServerConfig, ServiceName string, clusterName string, groupName string) (consumer *NacosConsumer, err error) {
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
		consumer.subscribed = false
		consumer.clusterName = clusterName //"cluster-default"
		consumer.groupName = groupName     //"group-default"
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
	consumer.subscribed = false
	consumer.clusterName = np.clusterName
	consumer.groupName = np.groupName
	return
}

//使用consumer的namingClient构建provider
func (nc *NacosConsumer) CreateNacosProvider(foo GrpcRegisterFunc, ServiceName string, serviceIp string, metadata *map[string]string) (provider *NacosProvider, err error) {
	provider = &NacosProvider{}
	provider.registerFunc = foo
	provider.clientConfig = nc.clientConfig
	provider.serverConfigs = nc.serverConfigs
	provider.namingClient = nc.namingClient
	provider.serviceName = ServiceName
	provider.clusterName = nc.clusterName
	provider.groupName = nc.groupName
	provider.ip = serviceIp      //"127.0.0.1"
	provider.metadata = metadata //&map[string]string{"idc": "shanghai"}
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

func (nc *NacosConsumer) GetServiceConnection() (conn *grpc.ClientConn, err error) {
	//链接状态异常，申请备用链接，如果备用链接全部不可用，则返回异常
	//先寻找备用线路是否有可用的
	for _, v := range nc.connList {
		if v != nil && (v.instance.GetState().String() != "SHUTDOWN" && v.instance.GetState().String() != "Invalid-State") {
			conn = v.instance
			break
		}
	}
	if conn == nil {
		//没有找到可用链接，直接创建新的
		instance, err1 := (*nc.namingClient).SelectOneHealthyInstance(vo.SelectOneHealthInstanceParam{
			ServiceName: nc.serviceName,
			GroupName:   nc.groupName,             // 默认值DEFAULT_GROUP
			Clusters:    []string{nc.clusterName}, // 默认值DEFAULT
		})
		if err1 != nil {
			err = err1
			return
		}
		co, err1 := nc.addNewConn(instance.Ip)
		if err1 != nil {
			err = err1
			return
		}
		conn = co.instance
	}
	if !nc.subscribed {
		current := atomic.AddInt64(&nc.subscribeCount, 1)
		if current == 1 {
			//订阅，每个服务只会订阅一次
			err := (*nc.namingClient).Subscribe(&vo.SubscribeParam{
				ServiceName: nc.serviceName,
				GroupName:   nc.groupName,             // 默认值DEFAULT_GROUP
				Clusters:    []string{nc.clusterName}, // 默认值DEFAULT
				SubscribeCallback: func(services []model.SubscribeService, err error) {
					fmt.Printf("Subscribe callback return services", err)
					//服务有变化重新检测链接
					err = nc.refreshServiceConn()
					fmt.Printf("refresh serviceConn error:", err)
				},
			})
			if err != nil {
				//订阅状态失败

				return nil, errors.New("Subscribe services fail:" + err.Error())
			} else {
				nc.subscribed = true
			}
			atomic.AddInt64(&nc.subscribeCount, -1)
		}
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
func (nc *NacosConsumer) getNewServiceConn() (*grpc.ClientConn, error) {
	current := atomic.AddInt64(&nc.process, 1)
	if current == 1 {
		instance, err1 := (*nc.namingClient).SelectOneHealthyInstance(vo.SelectOneHealthInstanceParam{
			ServiceName: nc.serviceName,
			GroupName:   nc.groupName,             // 默认值DEFAULT_GROUP
			Clusters:    []string{nc.clusterName}, // 默认值DEFAULT
		})
		if err1 != nil {
			atomic.AddInt64(&nc.process, -1)
			return nil, errors.New("reset connection fail:" + err1.Error())
		}
		conn, err1 := grpc.Dial(instance.Ip+":"+strconv.FormatUint(instance.Port, 10), grpc.WithInsecure())
		if err1 != nil {
			atomic.AddInt64(&nc.process, -1)
			return nil, errors.New("reset connection fail:" + err1.Error())
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
					err = nc.refreshMainServiceConn()
					fmt.Printf("refreshMainServiceConn error:", err)
				},
			})
			if err != nil {
				//订阅状态失败
				atomic.AddInt64(&nc.process, -1)
				conn.Close()
				return nil, errors.New("Subscribe services fail:" + err.Error())
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

//创建一个新链接取代当前主链接，并将旧的主链接转入备用线路，等待定时回收
func (nc *NacosConsumer) refreshServiceConn() error {
	var tmpConn *grpc.ClientConn
	current := atomic.AddInt64(&nc.refreshProcess, 1)
	if current == 1 {
		//只会有一条刷新处理中
		instance, err1 := (*nc.namingClient).SelectOneHealthyInstance(vo.SelectOneHealthInstanceParam{
			ServiceName: nc.serviceName,
			GroupName:   nc.groupName,             // 默认值DEFAULT_GROUP
			Clusters:    []string{nc.clusterName}, // 默认值DEFAULT
		})
		if err1 != nil {
			atomic.AddInt64(&nc.process, -1)
			return errors.New("reset connection fail:" + err1.Error())
		}
		//如果取得的ip在与主线程一致，则先关闭主线程
		if nc.conn.Target() != instance.Ip {
			tmpConn = nc.conn
			//断开主线路
			nc.conn = nil
			if tmpConn.GetState().String() != "SHUTDOWN" && tmpConn.GetState().String() != "Invalid-State" {
				//链接本来就已经失败了，直接关闭重新发起
				tmpConn.Close()
			} else {
				//当前主连接可能正在使用中，先等待一个timeout时间
				time.Sleep(time.Duration(nc.clientConfig.TimeoutMs) * time.Millisecond)
				tmpConn.Close()
			}
		} else {
			//从备用链接中检索ip
			conn := nc.findAndRemoveBackUpConn(instance.Ip)
			if conn == nil {
				//如果不存在备用线路,直接加一个备用线路,然后转移
				conn, err1 = nc.addNewConn(instance.Ip)
				if err1 != nil {
					//创建失败
					return err1
				}

			} else {
				//如果存在，直接升为主链
				if conn.GetState().String() != "SHUTDOWN" && conn.GetState().String() != "Invalid-State" {
					//链接本来就已经失败了，直接关闭重新发起
					conn.Close()
				} else {
					nc.conn = conn
				}
			}

		}
		//重新加入主连接

	}
	atomic.AddInt64(&nc.refreshProcess, -1)

}

//列表cas删除,必须符合ip地址
func (nc *NacosConsumer) findAndRemoveBackUpConn(ip string) (conn *ServiceConnection) {
	for i, v := range nc.connList {
		if v != nil && ip == v.instance.Target() {
			current := atomic.AddInt64(&v.processBtn, 1)
			conn = v
			if current == 1 {
				//可操作
				nc.connList[i] = nil
			}
			//不可操作重试
			atomic.AddInt64(&v.processBtn, -1)
			conn = nc.findAndRemoveBackUpConn(ip)
		}
	}
	return
}

//线程安全创建一个链接到备用池中,如果已经存在则直接返回既存实例
func (nc *NacosConsumer) addNewConn(ip string) (conn *ServiceConnection, err error) {
	//先寻找是否存在
	for i, v := range nc.connList {
		if v != nil && ip == v.instance.Target() {
			conn = v
		} else {
			//获取map的此操作key
			current := atomic.AddInt64(&nc.listBtn, 1)
			if current == 1 {
				//可操作
				instance, err1 := (*nc.namingClient).SelectOneHealthyInstance(vo.SelectOneHealthInstanceParam{
					ServiceName: nc.serviceName,
					GroupName:   nc.groupName,             // 默认值DEFAULT_GROUP
					Clusters:    []string{nc.clusterName}, // 默认值DEFAULT
				})
				if err1 != nil {
					atomic.AddInt64(&nc.listBtn, -1)
					err = errors.New("create connection fail:" + err1.Error())
				}
				conn, err1 := grpc.Dial(instance.Ip+":"+strconv.FormatUint(instance.Port, 10), grpc.WithInsecure())
				if err1 != nil {
					atomic.AddInt64(&nc.listBtn, -1)
					return nil, errors.New("reset connection fail:" + err1.Error())
				}
				nc.connList[i] = &ServiceConnection{
					instance:   conn,
					processBtn: 0,
				}
				atomic.AddInt64(&nc.listBtn, -1)
			} else {
				atomic.AddInt64(&nc.listBtn, -1)
				conn, err = nc.addNewConn(ip)
			}
		}
	}
	return
}
