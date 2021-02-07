package micro

import (
	"context"
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

var (
	//不同服务使用一个ip:port提供服务，复用链接
	connPool = map[string]*grpc.ClientConn{}
)

type ServiceConnection struct {
	address    string
	processBtn int64
}

func (s *ServiceConnection) GetConn() *grpc.ClientConn {
	return connPool[s.address]
}
func (s *ServiceConnection) SetConn(conn *grpc.ClientConn) {
	if conn == nil {
		s.address = ""
	} else {
		s.address = conn.Target()
	}
	connPool[s.address] = conn
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
	clientConfig         *constant.ClientConfig
	serverConfigs        *[]constant.ServerConfig
	namingClient         *naming_client.INamingClient
	serviceName          string
	connList             [5]*ServiceConnection
	clusterName          string
	groupName            string
	subscribed           bool
	subscribeCount       int64
	requestTimeoutSecond time.Duration
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

func CreateNacosConsumer(clientConfig *constant.ClientConfig, serverConfigs *[]constant.ServerConfig, ServiceName string, clusterName string, groupName string, timeoutSeconds int) (consumer *NacosConsumer, err error) {
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
		consumer.requestTimeoutSecond = time.Second * time.Duration(timeoutSeconds)
		consumer.connList = [5]*ServiceConnection{new(ServiceConnection), new(ServiceConnection), new(ServiceConnection), new(ServiceConnection), new(ServiceConnection)}
	}
	return
}

//使用provider的namingClient构建consumer
func (np *NacosProvider) CreateNacosConsumer(ServiceName string, timeoutSeconds int) (consumer *NacosConsumer) {
	consumer = &NacosConsumer{}
	consumer.clientConfig = np.clientConfig
	consumer.serverConfigs = np.serverConfigs
	consumer.namingClient = np.namingClient
	consumer.serviceName = ServiceName
	consumer.subscribed = false
	consumer.clusterName = np.clusterName
	consumer.groupName = np.groupName
	consumer.requestTimeoutSecond = time.Second * time.Duration(timeoutSeconds)
	consumer.connList = [5]*ServiceConnection{new(ServiceConnection), new(ServiceConnection), new(ServiceConnection), new(ServiceConnection), new(ServiceConnection)}
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
		if v.GetConn() != nil {
			fmt.Println(v.GetConn().GetState())
		}
		if v.GetConn() != nil && (v.GetConn().GetState().String() != "SHUTDOWN" && v.GetConn().GetState().String() != "Invalid-State" && v.GetConn().GetState().String() != "TRANSIENT_FAILURE" && v.GetConn().GetState().String() != "CONNECTING") {
			conn = v.GetConn()
			break
		} else if v.GetConn() != nil && (v.GetConn().GetState().String() == "SHUTDOWN" || v.GetConn().GetState().String() == "Invalid-State" || v.GetConn().GetState().String() == "TRANSIENT_FAILURE") {
			//释放已经失效的链接
			go nc.closeAndRemoveConn(v.GetConn().Target())
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
		co, err1 := nc.addNewConn(instance.Ip, strconv.FormatUint(instance.Port, 10))
		if err1 != nil {
			err = err1
			return
		}
		conn = co.GetConn()
	}
	//if !nc.subscribed {
	//	success := atomic.CompareAndSwapInt64(&nc.subscribeCount, 0, 1)
	//	if success {
	//		go func() {
	//			//订阅，只会订阅一次
	//			err1 := (*nc.namingClient).Subscribe(&vo.SubscribeParam{
	//				ServiceName: nc.serviceName,
	//				GroupName:   nc.groupName,             // 默认值DEFAULT_GROUP
	//				Clusters:    []string{nc.clusterName}, // 默认值DEFAULT
	//				SubscribeCallback: func(services []model.SubscribeService, err error) {
	//					//fmt.Println("Subscribe callback return services err", err)
	//					//fmt.Println("Subscribe callback return services", services)
	//					//服务有变化重新检测链接
	//					//for _, v := range services {
	//					//	_, err1 := nc.addNewConn(v.Ip, strconv.FormatUint(v.Port, 10))
	//					//	if err1 != nil {
	//					//		fmt.Println("add serviceConn error:", err1)
	//					//	}
	//					//}
	//				},
	//			})
	//			if err1 != nil {
	//				//订阅状态失败
	//				nc.subscribeCount = 0
	//				fmt.Println("Subscribe services fail:" + err1.Error())
	//			} else {
	//				nc.subscribed = true
	//			}
	//		}()
	//	}
	//}
	return
}

func (nc *NacosConsumer) GetServiceName() (serviceName string, err error) {
	serviceName = nc.serviceName
	return
}
func (nc *NacosConsumer) GetNewTimeoutContext() (ctx context.Context, cancel context.CancelFunc) {
	ctx, cancel = context.WithTimeout(context.TODO(), nc.requestTimeoutSecond)
	return
}
func (nc *NacosConsumer) Stop() (err error) {
	//取消订阅
	err = (*nc.namingClient).Unsubscribe(&vo.SubscribeParam{
		ServiceName: nc.serviceName,
		GroupName:   nc.groupName,             // 默认值DEFAULT_GROUP
		Clusters:    []string{nc.clusterName}, // 默认值DEFAULT
		SubscribeCallback: func(services []model.SubscribeService, err2 error) {
			fmt.Println("Unsubscribe services:", err2.Error())
		},
	})
	//释放所有链接
	for _, v := range nc.connList {
		if v.GetConn() != nil {
			//关闭链接的操作优先级最高。直接切断操作
			conn := v.GetConn()
			v.SetConn(nil)
			atomic.AddInt64(&v.processBtn, 1)
			go func() {
				//等待一个安全时间后关闭连接
				if conn.GetState().String() != "SHUTDOWN" && conn.GetState().String() != "Invalid-State" && conn.GetState().String() == "TRANSIENT_FAILURE" {
					//正常状态的链接需要关闭，等待一个服务失效时间
					time.Sleep(nc.requestTimeoutSecond)
					err = v.GetConn().Close()
				}
			}()
		}
	}
	return
}

//列表cas删除
func (nc *NacosConsumer) findAndRemoveConn(host string, retryTimes int) (conn *ServiceConnection) {
	for i, v := range nc.connList {
		if v.GetConn() != nil && host == v.GetConn().Target() {
			current := atomic.AddInt64(&v.processBtn, 1)
			if current == 1 {
				//获得操作权限后才可操作
				conn = v
				nc.connList[i] = nil
			} else if retryTimes > 0 {
				atomic.AddInt64(&v.processBtn, -1)
				//不可操作，递归重试，最多递归20次
				time.Sleep(time.Duration(100) * time.Millisecond)
				return nc.findAndRemoveConn(host, retryTimes-1)
			} else {
				//超出重试次数不成功，直接返回不存在
				conn = nil
			}
			atomic.AddInt64(&v.processBtn, -1)
			break
		}
	}
	return
}

//安全关闭并移除某个链接（保证正常业务操作已经完成）
func (nc *NacosConsumer) closeAndRemoveConn(host string) (err error) {
	for _, v := range nc.connList {
		if v.GetConn() != nil && host == v.GetConn().Target() {
			//关闭链接的操作优先级最高。直接切断操作
			conn := v.GetConn()
			v.SetConn(nil)
			//等待一个安全时间后关闭连接
			if conn.GetState().String() != "SHUTDOWN" && conn.GetState().String() != "Invalid-State" && conn.GetState().String() == "TRANSIENT_FAILURE" {
				//正常状态的链接需要关闭，等待一个服务失效时间
				time.Sleep(nc.requestTimeoutSecond)
				err = conn.Close()
				if err != nil {
					fmt.Println(err)
				}
			}
			break
		}
	}
	return
}

//线程安全创建一个链接到备用池中,如果已经存在则直接返回既存实例
func (nc *NacosConsumer) addNewConn(ip string, port string) (conn *ServiceConnection, err error) {
	//先寻找是否存在
	emptyIndex := -1
	for i, v := range nc.connList {
		if v.GetConn() != nil && (ip+":"+port) == v.GetConn().Target() {
			conn = v
			break
		} else if v.GetConn() == nil && emptyIndex == -1 && v.processBtn == 0 {
			//只存第一个空位
			emptyIndex = i
			fmt.Println("set " + strconv.Itoa(i))
		}
	}
	//不存在的进行新增
	if conn == nil {
		//获取map的此操作key
		if emptyIndex != -1 {
			emptyConn := nc.connList[emptyIndex]
			success := atomic.CompareAndSwapInt64(&emptyConn.processBtn, 0, 1)
			if success {
				//线路还有空余，且获取到了操作权
				grpcConn, err1 := grpc.Dial(ip+":"+port, grpc.WithInsecure())
				if err1 != nil {
					atomic.AddInt64(&emptyConn.processBtn, -1)
					return nil, errors.New("reset connection fail:" + err1.Error())
				}
				emptyConn.SetConn(grpcConn)
				conn = emptyConn
				atomic.AddInt64(&emptyConn.processBtn, -1)
			} else {
				//获取空位失败，重新获取一个空位
				conn, err = nc.addNewConn(ip, port)
			}
		} else {
			//无空余，不插入
			err = errors.New("no more connection can be create")
		}
	}
	return
}
