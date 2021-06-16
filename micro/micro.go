package micro

import (
	"context"
	"errors"
	"fmt"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"github.com/grpc-ecosystem/go-grpc-middleware/ratelimit"
	grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"net"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"
)

var (
	listener       net.Listener
	button         int64
	consumerButton int64
	consumerMap    = &map[string]*Consumer{}
	providerMap    = &map[string]*Provider{}
)

type CircuitController struct {
	CircuitConfig CircuitConfig
	//当前正在执行
	reqCount     int64
	timeoutCount int64
	//触发熔断后的重新开启时间，此段时间内如果继续超时则延后熔断结束时间、重试时间
	retryStart time.Time
	//熔断周期结束时间
	circuitEnd    time.Time
	circuitButton int32
}

func (c *CircuitController) AddReq(num int64) int64 {
	return atomic.AddInt64(&c.reqCount, num)
}
func (c *CircuitController) AddTimeout(num int64) int64 {
	if c.CircuitConfig.TimeoutThresholdRate == 0 {
		//不限制失败熔断,无需统计请求次数
		return 0
	}
	current := time.Now()
	timeout := atomic.AddInt64(&c.timeoutCount, num)
	if num > 0 && c.reqCount > 0 && c.reqCount > c.CircuitConfig.CircuitBaseCount {
		//计算比率是否大于限定
		defer func() {
			if r := recover(); r != nil {
				switch r.(type) {
				case runtime.Error:
				default:
				}
			}
		}()
		if (float64(c.timeoutCount) / float64(c.reqCount)) > c.CircuitConfig.TimeoutThresholdRate {
			if current.After(c.circuitEnd) {
				//触发熔断
				c.circuitEnd = current.Add(c.CircuitConfig.CircuitDuration)
				c.retryStart = current.Add(c.CircuitConfig.RetryDuration)
			} else if atomic.CompareAndSwapInt32(&c.circuitButton, 0, 1) {
				//如果已经在熔断中了，则延长周期
				defer atomic.CompareAndSwapInt32(&c.circuitButton, 1, 0)
				c.circuitEnd = current.Add(c.CircuitConfig.CircuitDuration)
				c.retryStart = current.Add(c.CircuitConfig.RetryDuration)
			}
		}
	}
	return timeout
}

func (c *CircuitController) Limit() bool {
	if c.CircuitConfig.TimeoutThresholdRate == 0 {
		//不限制失败熔断,无需统计请求次数
		return false
	}
	current := time.Now()
	if current.After(c.circuitEnd) {
		//已经不在熔断周期内,添加计数
		reqCount := c.AddReq(1)
		fmt.Println("reqCount invoke:" + strconv.FormatInt(reqCount, 10))
		return false
	} else {
		//处于熔断时间，判断是否重试
		if current.After(c.retryStart) {
			//可以重试但是不需要增加计数，如果此时还是失败，则直接延长熔断时间
			reqCount := c.AddReq(1)
			fmt.Println("reqCount retryStart:" + strconv.FormatInt(reqCount, 10))
			return false
		}
	}
	return true
}

//超时计数器
func CircuitBreakerIncr(circuitController *CircuitController) grpc_zap.Option {
	return grpc_zap.WithDurationField(func(duration time.Duration) zapcore.Field {
		if circuitController.CircuitConfig.TimeoutThresholdRate > 0 {
			//不限制失败熔断,无需统计请求次数
			if duration > circuitController.CircuitConfig.TimeoutDuration {
				//超时了
				circuitController.AddTimeout(1)
				go func() {
					//熔断判断周期内保理此次超时记录
					time.Sleep(circuitController.CircuitConfig.CircuitDuration)
					reqCount := circuitController.AddReq(-1)
					timeoutCount := circuitController.AddTimeout(-1)
					fmt.Println("reqCount timeout_return:" + strconv.FormatInt(reqCount, 10))
					fmt.Println("timeoutCount timeout_return:" + strconv.FormatInt(timeoutCount, 10))
				}()
			} else {
				//正常请求
				reqCount := circuitController.AddReq(-1)
				fmt.Println("reqCount normal_return:" + strconv.FormatInt(reqCount, 10))
			}
		}
		return zap.Float32("grpc.time_ms", float32(duration.Nanoseconds()/1000)/1000)
	})
}

type CircuitConfig struct {
	//熔断超时率，大于此数值时设置熔断时间，默认按照1分钟内的请求来计算
	TimeoutThresholdRate float64
	//熔断触发时持续时间
	TimeoutDuration time.Duration
	//熔断时重试发起的尝试时间，在此期间如果依然失败，则直接延长熔断时间
	RetryDuration time.Duration
	//熔断触发时持续时间
	CircuitDuration time.Duration
	//从指定数量开始做熔断判断
	CircuitBaseCount int64
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
func CreateProvider(iProvider IProvider, CircuitConfig CircuitConfig, Logger *zap.Logger) (*Provider, error) {
	provider := Provider{
		IProvider: iProvider,
	}
	circuitController := CircuitController{
		CircuitConfig: CircuitConfig,
	}
	provider.Logger = Logger
	provider.circuitController = circuitController
	return &provider, nil
}

type IConsumer interface {
	GetServiceConnection() (conn *grpc.ClientConn, err error)
	GetServiceName() (serviceName string, err error)
	Stop() (err error)
}

type Consumer struct {
	IConsumer
	requestTimeoutSecond time.Duration
}

func (c *Consumer) GetNewTimeoutContext() (ctx context.Context, cancel context.CancelFunc) {
	ctx, cancel = context.WithTimeout(context.TODO(), c.requestTimeoutSecond)
	return
}
func CreateConsumer(iConsumer IConsumer) (*Consumer, error) {
	consumer := Consumer{
		IConsumer: iConsumer,
	}
	return &consumer, nil
}

func RecoveryInterceptor() grpc_recovery.Option {
	return grpc_recovery.WithRecoveryHandler(func(p interface{}) (err error) {
		switch p.(type) {
		case runtime.Error:
			// 运行时错误
			err = p.(error)
			fmt.Println(string(debug.Stack()))
			return err
		default:
			// 非运行时错误
		}
		return grpc.Errorf(codes.Unknown, "panic triggered: %v", p)
	})
}

//在指定端口监听微服务，并将端口注册给注册中心
func StartProvide(provider *Provider, port string) (err error) {
	if listener == nil {
		err = createListener(port)
		if err != nil {
			return
		}
	}
	var server *grpc.Server
	if provider.Logger == nil {
		//设置默认logger，输出到控制台
		provider.Logger = getDefaultLogger()
	}
	server = grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
		}),
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
			ratelimit.UnaryServerInterceptor(provider.GetCircuitController()),
			grpc_zap.UnaryServerInterceptor(provider.Logger, CircuitBreakerIncr(provider.GetCircuitController())),
			grpc_recovery.UnaryServerInterceptor(RecoveryInterceptor()),
		)),
		grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
			ratelimit.StreamServerInterceptor(provider.GetCircuitController()),
			grpc_zap.StreamServerInterceptor(provider.Logger, CircuitBreakerIncr(provider.GetCircuitController())),
			grpc_recovery.StreamServerInterceptor(RecoveryInterceptor()),
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
func AddConsumer(consumer *Consumer) (err error) {
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
