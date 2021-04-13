package micro

import (
	"errors"
	"fmt"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"github.com/grpc-ecosystem/go-grpc-middleware/ratelimit"
	grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"net"
	"os"
	"runtime"
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

type CircuitController struct {
	CircuitConfig CircuitConfig
	//本轮计时周期的
	expireEnd     time.Time
	expireEndLock int32
	reqCount      int64
	timeoutCount  int64

	//触发熔断后的重新开启时间，此段时间内如果继续超时则延后熔断结束时间、重试时间
	retryStart time.Time
	//熔断周期结束时间
	circuitEnd time.Time
}

//如果时间周期还没到，则增加，否则自动置为1
func (c *CircuitController) addReq(num int64, now *time.Time) int64 {
	if now.After(c.expireEnd) && atomic.CompareAndSwapInt32(&c.expireEndLock, 0, 1) {
		//过了上个计时周期,并且抢到了新的计时启动锁
		defer atomic.CompareAndSwapInt32(&c.expireEndLock, 1, 0)
		//TODO 按照当前时间配置增加时间
		c.reqCount = 0
		c.timeoutCount = 0
	} else {
		return atomic.AddInt64(&c.reqCount, num)
	}
	return 0
}
func (c *CircuitController) AddTimeout(num int64) int64 {
	if c.CircuitConfig.timeoutThresholdRate == 0 {
		//不限制失败熔断,无需统计请求次数
		return 0
	}
	timeout := atomic.AddInt64(&c.timeoutCount, num)
	if c.reqCount > 0 {
		//计算比率是否大于限定
		defer func() {
			if r := recover(); r != nil {
				switch r.(type) {
				case runtime.Error:
				default:
				}
			}
		}()
		if (float64(c.timeoutCount) / float64(c.reqCount)) > c.CircuitConfig.timeoutThresholdRate {
			//TODO 熔断

		}
	}
	return timeout
}

func (c *CircuitController) Limit() bool {
	if c.CircuitConfig.timeoutThresholdRate == 0 {
		//不限制失败熔断,无需统计请求次数
		return false
	}
	current := time.Now()
	if current.After(c.circuitEnd) {
		//已经不在熔断周期内,添加计数
		c.addReq(1, &current)
		return false
	} else {
		//处于熔断时间，判断是否重试
		if current.After(c.retryStart) {
			//可以重试但是不需要增加计数
			return false
		}
	}
	return true
}

//失败计数器
func CircuitBreakerIncr(circuitController *CircuitController) grpc_recovery.Option {
	return grpc_recovery.WithRecoveryHandler(func(p interface{}) (err error) {
		//TODO 中单位时间内超时的次数超过指定数量，需要先判断出是超时错误
		circuitController.AddTimeout(1)
		return nil
	})
}

type CircuitConfig struct {
	//TODO 熔断配置，单位时间内错误率高于某比率时自动熔断，并在指定时间后重试
	//熔断超时率，大于此数值时设置熔断时间
	timeoutThresholdRate float64
}

type GrpcRegisterFunc func(s *grpc.Server)
type IProvider interface {
	RegisterServices(s *grpc.Server, port string) (err error)
	GetServiceName() (serviceName string)
	DeregisterServices() (err error)
}

type Provider struct {
	IProvider
	Logger            *zap.Logger
	circuitController CircuitController
}

func (p *Provider) GetCircuitController() *CircuitController {
	return &p.circuitController
}
func CreateProvider(iProvider IProvider, CircuitConfig CircuitConfig, Logger *zap.Logger) *Provider {
	provider := Provider{
		IProvider: iProvider,
	}
	circuitController := CircuitController{
		CircuitConfig: CircuitConfig,
	}
	provider.Logger = Logger
	provider.circuitController = circuitController
	return &provider
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
	if provider.Logger != nil {
		//设置默认logger，输出到控制台
		provider.Logger = getDefaultLogger()
	}
	server = grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
		}),
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
			ratelimit.UnaryServerInterceptor(provider.GetCircuitController()),
			grpc_zap.UnaryServerInterceptor(provider.Logger),
			grpc_recovery.UnaryServerInterceptor(CircuitBreakerIncr(provider.GetCircuitController())),
		)),
		grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
			ratelimit.StreamServerInterceptor(provider.GetCircuitController()),
			grpc_zap.StreamServerInterceptor(provider.Logger),
			grpc_recovery.StreamServerInterceptor(CircuitBreakerIncr(provider.GetCircuitController())),
		)),
	)
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
	return grpc_recovery.WithRecoveryHandler(func(p interface{}) error {
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
	if provider.Logger != nil {
		//设置默认logger，输出到控制台
		provider.Logger = getDefaultLogger()
	}
	server = grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
		}),
		grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
			grpc_zap.StreamServerInterceptor(provider.Logger),
			grpc_recovery.StreamServerInterceptor(recoveryInterceptor(panicErr)),
		)),
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
			grpc_zap.UnaryServerInterceptor(provider.Logger),
			grpc_recovery.UnaryServerInterceptor(recoveryInterceptor(panicErr)),
		)),
	)
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

func getDefaultLogger() *zap.Logger {
	encoder := zapcore.NewConsoleEncoder(zapcore.EncoderConfig{
		MessageKey:  "msg",
		LevelKey:    "level",
		EncodeLevel: zapcore.CapitalLevelEncoder,
		TimeKey:     "ts",
		EncodeTime: func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format("2006-01-02 15:04:05.000"))
		},
		CallerKey:    "file",
		EncodeCaller: zapcore.ShortCallerEncoder,
		EncodeDuration: func(d time.Duration, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendInt64(int64(d) / 1000000)
		},
	})
	infoLevel := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl < zapcore.WarnLevel
	})
	core := zapcore.NewTee(
		zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), infoLevel),
	)
	return zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
}
