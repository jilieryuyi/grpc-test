package proxy

// Code generated by protoc-gen-grpc-gateway. DO NOT EDIT.
// source: addsvc.proto

/*
Package pb is a reverse proxy.

It translates gRPC into RESTful JSON APIs.
*/

import (
	"net/http"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"fmt"
	"encoding/json"
	"strings"
	"github.com/jilieryuyi/grpc-gateway/service"
	"github.com/jilieryuyi/grpc-gateway/proto"
	//"time"
	"github.com/hashicorp/consul/api"
	log "github.com/sirupsen/logrus"
	"time"
)


type connection struct {
	conn *grpc.ClientConn
	start int64
}

type MyMux struct {
	conns map[string]*connection//*grpc.ClientConn
	ctx context.Context
	consulAddress string
	health *api.Health
}

func NewMyMux(ctx context.Context,consulAddress string) *MyMux {
	config := api.DefaultConfig()
	config.Address = consulAddress
	client, err := api.NewClient(config)
	if err != nil {
		log.Panicf("%v", err)
	}
	m := &MyMux{
		ctx : ctx,
		conns: make(map[string]*connection),
		consulAddress:consulAddress,
		health: client.Health(),
	}
	return m
}

type URI struct {
	packageName string
	serviceName string
	version string
	method string
}

func (p *MyMux) serviceExists(serviceName string) bool {
	cs, _, err := p.health.Service(serviceName, "", true, nil)
	if err != nil {
		log.Errorf("%v", err)
		return false
	}
	//for _, s := range cs {
	//	s.Service.
	//	// addr should like: 127.0.0.1:8001
	//	//addrs = append(addrs, fmt.Sprintf("%s:%d", s.Service.Address, s.Service.Port))
	//}
	//return addrs, meta.LastIndex, nil
	return len(cs) > 0
}

func (p *MyMux) getGrpcClient(serviceName string) *connection {
	//address := "localhost:8082"//服务发现的地址
	//opt1 := grpc.WithDefaultCallOptions(grpc.CallCustomCodec(MyCodec(encoding.GetCodec(proto.Name))))
	////opt2 := grpc.WithDefaultCallOptions(grpc.CallContentSubtype("proto"))
	//opts = append(opts, opt1)
	////opts = append(opts, opt2)
	////grpc.NewContextWithServerTransportStream()
	//mux.conn, err = grpc.Dial(address, opts...)

	//clear timeout conn
	// 最长时间缓存nil的client 3秒
	// 防止穿透，一直查询consul
	for key, v := range p.conns {
		if v.conn == nil && time.Now().Unix()-v.start > 3 {
			delete(p.conns, key)
		}
	}

	conn, ok := p.conns[serviceName]
	// 使用连接池
	if ok && conn.conn != nil {
		fmt.Printf("http proxy use pool\n")
		return conn
	}

	if ok && conn.conn == nil {
		fmt.Printf("http proxy use pool 2\n")
		return conn
	}

	conn = &connection{conn:nil, start:time.Now().Unix()}
	p.conns[serviceName] = conn
	if !p.serviceExists(serviceName) {
		return conn
	}

	resl   := service.NewResolver(p.consulAddress)
	rr     := grpc.RoundRobin(resl)
	lb     := grpc.WithBalancer(rr)

	gconn, err := grpc.DialContext(p.ctx, serviceName, grpc.WithDefaultCallOptions(grpc.CallCustomCodec(proto.Codec()), grpc.FailFast(false)), grpc.WithInsecure(), lb)
	if err != nil {
		fmt.Printf("http proxy use err nil\n")
		return conn
	}
	conn.conn = gconn
	return conn
}

func (p *MyMux) Close() {
	for _, v := range p.conns {
		if v.conn != nil {
			v.conn.Close()
		}
	}
}

func (uri *URI) getServiceName() string {
	st := strings.Split(uri.serviceName, ".")
	serviceName := ""
	for _, v := range st {
		serviceName += strings.ToUpper(v[:1]) + v[1:]
	}
	return fmt.Sprintf("%v.%v", uri.packageName, serviceName)
}

func (uri *URI) getMethod() string {
	return strings.ToUpper(uri.method[:1]) + uri.method[1:]
}

func (p *MyMux) parseURL(url string) *URI {
	// /proto/service.add/v1/sum
	st := strings.Split(url, "/")
	if len(st) < 5 {
		return nil
	}
	return &URI{
		packageName:st[1],
		serviceName:st[2],
		version:st[3],
		method:st[4],
	}
}


func (p *MyMux) parseParams(req *http.Request) map[string]interface{} {
	req.ParseForm()
	//if strings.ToLower(req.Header.Get("Content-Type")) == "application/json" {
	// 处理传统意义上表单的参数，这里添加body内传输的json解析支持
	// 解析后的值默认追加到表单内部

	params := make(map[string]interface{})
	for key, v := range req.Form {
		params[key] = v[0]
	}
	if req.ContentLength <= 0 {
		return params
	}

	var data map[string]interface{}
	buf := make([]byte, req.ContentLength)
	n , err := req.Body.Read(buf)
	if err != nil || n <= 0 {
		fmt.Printf("req.Body read error: %v\n", err)
		return params
	}
	err = json.Unmarshal(buf, &data)
	if err != nil || data == nil {
		return params
	}
	for k, dv := range data {
		params[k] = dv
	}
	return params
}

func (p *MyMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	// url like:
	// http://localhost:8084/proto/service.add/v1/sum
	// package name is: proto
	// service name is: service.add
	// version is: v1
	// method is: sum
	fmt.Printf("%+v\n", *r)
	fmt.Printf("url: %+v\n", *r.URL)

	uri := p.parseURL(r.URL.Path)
	if uri == nil {
		w.Write([]byte("url path error, url path must be format by: /{packagename}/{servicename}/{version}/{method}"))
		return
	}
	fmt.Printf("uri: %+v\n", *uri)
	params := p.parseParams(r)

	fullMethod := fmt.Sprintf("/%v/%v", uri.getServiceName(), uri.getMethod())
	fmt.Printf("fullMethod=%s\v", fullMethod)

	conn := p.getGrpcClient(uri.serviceName)
	if conn == nil || conn.conn == nil {
		w.Write([]byte("connect "+uri.serviceName + " error"))
		return
	}
	var out interface{}
	err := conn.conn.Invoke(context.Background(), fullMethod, params, &out, grpc.FailFast(false))
	fmt.Printf("return: %+v, error: %+v\n", out, err)
	b, _:=json.Marshal(out)
	w.Write(b)
	return
}