package micro

import (
	"errors"
	"fmt"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	grpc_opentracing "github.com/grpc-ecosystem/go-grpc-middleware/tracing/opentracing"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"net"
	"sync/atomic"
	"time"
)

var (
	listener       net.Listener
	button         int64
	consumerButton int64
	consumerMap    = &map[string]Consumer{}
	providerMap    = &map[string]Provider{}
)

//失败计数器
func CircuitBreakerIncr(expire time.Duration) grpc_recovery.Option {
	return grpc_recovery.WithRecoveryHandler(func(p interface{}) (err error) {
		//TODO redis中单位时间内超时的次数超过指定数量
		return nil
	})
}

type CircuitConfig struct {
	//TODO 熔断配置，单位时间内错误率高于某比率时自动熔断，并在指定时间后重试
}

type GrpcRegisterFunc func(s *grpc.Server)
type Provider interface {
	RegisterServices(s *grpc.Server, port string) (err error)
	GetServiceName() (serviceName string)
	DeregisterServices() (err error)
	SetLogger(*zap.Logger) (err error)
	GetLogger() (logger *zap.Logger)
	GetCircuitConfig() (config *CircuitConfig)
}
type Consumer interface {
	GetServiceConnection() (conn *grpc.ClientConn, err error)
	GetServiceName() (serviceName string, err error)
	Stop() (err error)
}

//在指定端口监听微服务，并将端口注册给注册中心
func StartProvide(provider Provider, port string) (err error) {
	if listener == nil {
		err = createListener(port)
		if err != nil {
			return
		}
	}
	var server *grpc.Server
	if provider.GetLogger() != nil {
		server = grpc.NewServer(
			grpc.KeepaliveParams(keepalive.ServerParameters{
				MaxConnectionIdle: 5 * time.Minute,
			}),
			grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
				grpc_opentracing.UnaryServerInterceptor(),
				grpc_zap.UnaryServerInterceptor(provider.GetLogger()),
				grpc_recovery.UnaryServerInterceptor(CircuitBreakerIncr()),
			)),
			grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
				grpc_opentracing.StreamServerInterceptor(),
				grpc_zap.StreamServerInterceptor(provider.GetLogger()),
				grpc_recovery.StreamServerInterceptor(CircuitBreakerIncr()),
			)),
		)
	} else {
		server = grpc.NewServer(
			grpc.KeepaliveParams(keepalive.ServerParameters{
				MaxConnectionIdle: 5 * time.Minute,
			}),
			grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
				grpc_recovery.UnaryServerInterceptor(),
			)),
			grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
				grpc_recovery.StreamServerInterceptor(),
			)),
		)
	}
	//将该端口注册到注册中心，包含的服务由provider自行确定
	err = (provider).RegisterServices(server, port)
	if err != nil {
		return
	}
	(*providerMap)[provider.GetServiceName()] = provider
	defer func() {
		fmt.Println("stop micro service provider....")
		err := (provider).DeregisterServices()
		if err != nil {
			fmt.Println("Deregister Services fail:", err.Error())
		}
	}()
	err = server.Serve(listener)
	if err != nil {
		return
	}
	return
}
func recoveryInterceptor(err error) grpc_recovery.Option {
	return grpc_recovery.WithRecoveryHandler(func(p interface{}) (err error) {
		return err
	})
}

//指定grpc panic的服务启动方式
func StartProvideWithPanicCode(provider Provider, port string, panicErr error) (err error) {
	if listener == nil {
		err = createListener(port)
		if err != nil {
			return
		}
	}
	var server *grpc.Server
	if provider.GetLogger() != nil {
		server = grpc.NewServer(
			grpc.KeepaliveParams(keepalive.ServerParameters{
				MaxConnectionIdle: 5 * time.Minute,
			}),
			grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
				grpc_zap.StreamServerInterceptor(provider.GetLogger()),
				grpc_recovery.StreamServerInterceptor(recoveryInterceptor(panicErr)),
			)),
			grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
				grpc_zap.UnaryServerInterceptor(provider.GetLogger()),
				grpc_recovery.UnaryServerInterceptor(recoveryInterceptor(panicErr)),
			)),
		)
	} else {
		server = grpc.NewServer(grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
		}))
	}
	//将该端口注册到注册中心，包含的服务由provider自行确定
	err = (provider).RegisterServices(server, port)
	if err != nil {
		return
	}
	(*providerMap)[provider.GetServiceName()] = provider
	defer func() {
		fmt.Println("stop micro service provider....")
		err := (provider).DeregisterServices()
		if err != nil {
			fmt.Println("Deregister Services fail:", err.Error())
		}
	}()
	err = server.Serve(listener)
	if err != nil {
		return
	}
	return
}
func Stop() {
	if len(*consumerMap) != 0 {
		fmt.Println("stop micro service consumer....")
		for k := range *consumerMap {
			(*consumerMap)[k].Stop()
		}
	}
	if len(*providerMap) != 0 {
		fmt.Println("stop micro service provider....")
		for k := range *providerMap {
			(*providerMap)[k].DeregisterServices()
		}
	}
}
func AddConsumer(consumer Consumer) (err error) {
	serviceName, err := consumer.GetServiceName()
	if err != nil {
		return
	}
	if (*consumerMap)[serviceName] == nil {
		current := atomic.AddInt64(&consumerButton, 1) //加操作
		if current == 1 {
			//只允许单例操作
			(*consumerMap)[serviceName] = consumer
		} else {
			if (*consumerMap)[serviceName] != nil {
				//如果此时另一方已经加完了，则认为正常
				return
			}
			err = errors.New("AddConsumer fail, multi task is running")
		}
		atomic.AddInt64(&consumerButton, -1)
	}
	return
}
func GetServiceConn(serviceName string) (conn *grpc.ClientConn, err error) {
	consumer := (*consumerMap)[serviceName]
	if consumer == nil {
		err = errors.New("serviceName not found")
		return
	}
	return consumer.GetServiceConnection()
}
func DeleteConsumer(serviceName string) {
	delete(*consumerMap, serviceName)
}

func createListener(port string) (err error) {
	current := atomic.AddInt64(&button, 1) //加操作
	if current == 1 {
		listener, err = net.Listen("tcp", ":"+port)
		if err != nil {
			current = 0
		}
	} else {
		//等候其他线程创建,至多等待一分钟
		for i := 0; i < 6; i++ {
			time.Sleep(time.Second * 5)
			if listener != nil {
				return
			}
		}
		//创建失败
		err = errors.New("create micro service Listener fail")
	}
	return
}
