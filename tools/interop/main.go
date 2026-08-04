package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	collaborationv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration/collaborationservice"
	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	knowledgev1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge/knowledgeservice"
	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/cloudwego/gopkg/bufiox"
	kitexclient "github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/client/callopt"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/remote/trans/gonet"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/pkg/transmeta"
	kitexserver "github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/transport"
)

const (
	accessTokenKey = "knowledge-core-access-token"
	requestIDKey   = "x-request-id"
	traceParentKey = "traceparent"
	traceStateKey  = "tracestate"
	baggageKey     = "baggage"
)

type fixtureEnvironment struct {
	address       string
	token         string
	requestID     string
	traceParent   string
	traceState    string
	baggage       string
	traceID       string
	tlsCAFile     string
	tlsCertFile   string
	tlsKeyFile    string
	tlsServerName string
	serverTimeout time.Duration
	clientTimeout time.Duration
	delay         time.Duration
	deadline      time.Duration
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Kitex/Volo interoperability fixture failed: %v\n", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) != 1 {
		return errors.New("usage: go run ./tools/interop <certs|server|client>")
	}
	switch arguments[0] {
	case "certs":
		directory, err := requireEnvironment("KC_INTEROP_CERT_DIR")
		if err != nil {
			return err
		}
		return generateCertificates(directory)
	case "server":
		return runServer()
	case "client":
		return runClient()
	default:
		return fmt.Errorf("unknown fixture mode %q", arguments[0])
	}
}

func runServer() error {
	environment, err := loadFixtureEnvironment(false)
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", environment.address)
	if err != nil {
		return fmt.Errorf("listen for Go Knowledge fixture: %w", err)
	}
	tlsConfig, err := serverTLSConfig(environment)
	if err != nil {
		_ = listener.Close()
		return err
	}
	listener = tls.NewListener(listener, tlsConfig)
	server := kitexserver.NewServer(
		kitexserver.WithListener(listener),
		kitexserver.WithServerBasicInfo(&rpcinfo.EndpointBasicInfo{ServiceName: "knowledge-core.knowledge"}),
		kitexserver.WithReadWriteTimeout(environment.serverTimeout),
		kitexserver.WithEnableContextTimeout(true),
		kitexserver.WithCompatibleMiddlewareForUnary(),
		kitexserver.WithMetaHandler(transmeta.ServerTTHeaderHandler),
		kitexserver.WithTransServerFactory(gonet.NewTransServerFactory()),
		kitexserver.WithTransHandlerFactory(gonet.NewSvrTransHandlerFactory()),
	)
	handler := &knowledgeFixture{
		expectedToken:       environment.token,
		expectedRequestID:   environment.requestID,
		expectedTraceParent: environment.traceParent,
		expectedTraceState:  environment.traceState,
		expectedBaggage:     environment.baggage,
		expectedTraceID:     environment.traceID,
		delay:               environment.delay,
	}
	if err := knowledgeservice.RegisterService(server, handler); err != nil {
		_ = listener.Close()
		return fmt.Errorf("register Go Knowledge fixture: %w", err)
	}
	fmt.Printf("READY %s\n", listener.Addr())
	if err := server.Run(); err != nil {
		return fmt.Errorf("serve Go Knowledge fixture: %w", err)
	}
	return nil
}

func runClient() error {
	environment, err := loadFixtureEnvironment(true)
	if err != nil {
		return err
	}
	tlsConfig, err := clientTLSConfig(environment)
	if err != nil {
		return err
	}
	client, err := collaborationservice.NewClient(
		"knowledge-core.collaboration",
		kitexclient.WithHostPorts(environment.address),
		kitexclient.WithConnectTimeout(environment.clientTimeout),
		kitexclient.WithRPCTimeout(environment.clientTimeout),
		kitexclient.WithTransportProtocol(transport.TTHeader),
		kitexclient.WithMetaHandler(transmeta.ClientTTHeaderHandler),
		kitexclient.WithDialer(tlsDialer{config: tlsConfig}),
		kitexclient.WithTransHandlerFactory(gonet.NewCliTransHandlerFactory()),
	)
	if err != nil {
		return fmt.Errorf("create Go Collaboration fixture client: %w", err)
	}
	ctx := outgoingContext(environment)
	okMessage := "ok"
	response, err := client.Ping(ctx, &commonv1.PingRequest{Message: &okMessage})
	if err != nil {
		return fmt.Errorf("call Rust fixture Ping: %w", err)
	}
	if response == nil || response.Service != "collaboration" || response.Status != "ready" {
		return fmt.Errorf("rust fixture returned an invalid Ping response: %#v", response)
	}

	bizMessage := "biz"
	_, err = client.Ping(ctx, &commonv1.PingRequest{Message: &bizMessage})
	if err := validateBusinessError(
		err,
		collaborationv1.CodeInvalidInput,
		"collaboration.invalid_input",
		environment,
	); err != nil {
		return fmt.Errorf("validate Rust fixture BizStatus: %w", err)
	}

	delayMessage := "delay"
	_, err = client.Ping(
		ctx,
		&commonv1.PingRequest{Message: &delayMessage},
		callopt.WithRPCTimeout(environment.deadline),
	)
	if err == nil {
		return errors.New("rust fixture did not enforce the Kitex client deadline")
	}
	if _, business := kerrors.FromBizStatusError(err); business {
		return fmt.Errorf("rust fixture deadline returned a business error: %w", err)
	}
	fmt.Println("CLIENT_OK")
	return nil
}

type knowledgeFixture struct {
	knowledgev1.KnowledgeService
	expectedToken       string
	expectedRequestID   string
	expectedTraceParent string
	expectedTraceState  string
	expectedBaggage     string
	expectedTraceID     string
	delay               time.Duration
}

func (f *knowledgeFixture) Ping(ctx context.Context, request *commonv1.PingRequest) (*commonv1.PingResponse, error) {
	if err := f.verifyMetadata(ctx); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, f.businessError("knowledge.invalid_input")
	}
	switch request.GetMessage() {
	case "ok":
		return readyResponse("knowledge"), nil
	case "biz":
		return nil, f.businessError("knowledge.invalid_input")
	case "delay":
		timer := time.NewTimer(f.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			return readyResponse("knowledge"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	default:
		return nil, f.businessError("knowledge.invalid_input")
	}
}

func (f *knowledgeFixture) Live(ctx context.Context, request *commonv1.PingRequest) (*commonv1.PingResponse, error) {
	if err := f.verifyMetadata(ctx); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, f.businessError("knowledge.invalid_input")
	}
	return &commonv1.PingResponse{
		Service: "knowledge", Status: "live", UnixTime: time.Now().UTC().Unix(),
	}, nil
}

func (f *knowledgeFixture) verifyMetadata(ctx context.Context) error {
	expected := map[string]string{
		accessTokenKey: f.expectedToken,
		requestIDKey:   f.expectedRequestID,
		traceParentKey: f.expectedTraceParent,
		traceStateKey:  f.expectedTraceState,
		baggageKey:     f.expectedBaggage,
	}
	for key, want := range expected {
		got, exists := metainfo.GetPersistentValue(ctx, key)
		if !exists || got != want {
			return f.businessError("knowledge.invalid_input")
		}
	}
	return nil
}

func (f *knowledgeFixture) businessError(key string) error {
	return kerrors.NewBizStatusErrorWithExtra(
		knowledgev1.CodeInvalidInput,
		"invalid knowledge input",
		map[string]string{
			"error_key":  key,
			"error_kind": "invalid_argument",
			"request_id": f.expectedRequestID,
			"trace_id":   f.expectedTraceID,
		},
	)
}

func readyResponse(service string) *commonv1.PingResponse {
	return &commonv1.PingResponse{Service: service, Status: "ready", UnixTime: time.Now().UTC().Unix()}
}

func outgoingContext(environment fixtureEnvironment) context.Context {
	ctx := context.Background()
	for key, value := range map[string]string{
		accessTokenKey: environment.token,
		requestIDKey:   environment.requestID,
		traceParentKey: environment.traceParent,
		traceStateKey:  environment.traceState,
		baggageKey:     environment.baggage,
	} {
		ctx = metainfo.WithPersistentValue(ctx, key, value)
	}
	return ctx
}

func validateBusinessError(err error, code int32, key string, environment fixtureEnvironment) error {
	business, ok := kerrors.FromBizStatusError(err)
	if !ok {
		return fmt.Errorf("expected BizStatus, got %w", err)
	}
	if business.BizStatusCode() != code {
		return fmt.Errorf("business code = %d, want %d", business.BizStatusCode(), code)
	}
	expected := map[string]string{
		"error_key":  key,
		"error_kind": "invalid_argument",
		"request_id": environment.requestID,
		"trace_id":   environment.traceID,
	}
	for extraKey, want := range expected {
		if got := business.BizExtra()[extraKey]; got != want {
			return fmt.Errorf("business extra %q = %q, want %q", extraKey, got, want)
		}
	}
	return nil
}

func clientTLSConfig(environment fixtureEnvironment) (*tls.Config, error) {
	ca, err := os.ReadFile(environment.tlsCAFile)
	if err != nil {
		return nil, fmt.Errorf("read fixture CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("parse fixture CA: no certificates found")
	}
	certificate, err := tls.LoadX509KeyPair(environment.tlsCertFile, environment.tlsKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load fixture client certificate: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		Certificates: []tls.Certificate{certificate},
		ServerName:   environment.tlsServerName,
	}, nil
}

func serverTLSConfig(environment fixtureEnvironment) (*tls.Config, error) {
	ca, err := os.ReadFile(environment.tlsCAFile)
	if err != nil {
		return nil, fmt.Errorf("read fixture CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("parse fixture CA: no certificates found")
	}
	certificate, err := tls.LoadX509KeyPair(environment.tlsCertFile, environment.tlsKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load fixture server certificate: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientCAs:    roots,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, nil
}

type tlsDialer struct{ config *tls.Config }

func (d tlsDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	connection, err := tls.DialWithDialer(dialer, network, address, d.config.Clone())
	if err != nil {
		return nil, err
	}
	return newGonetTLSConn(connection), nil
}

type gonetTLSConn struct {
	net.Conn
	reader *bufiox.DefaultReader
	writer *bufiox.DefaultWriter
}

func newGonetTLSConn(connection net.Conn) *gonetTLSConn {
	return &gonetTLSConn{
		Conn:   connection,
		reader: bufiox.NewDefaultReader(connection),
		writer: bufiox.NewDefaultWriter(connection),
	}
}

func (c *gonetTLSConn) Reader() *bufiox.DefaultReader { return c.reader }

func (c *gonetTLSConn) Writer() *bufiox.DefaultWriter { return c.writer }

func loadFixtureEnvironment(forClient bool) (fixtureEnvironment, error) {
	values := make(map[string]string)
	required := []string{
		"KC_INTEROP_ADDRESS",
		"KC_INTEROP_EXPECT_TOKEN",
		"KC_INTEROP_EXPECT_REQUEST_ID",
		"KC_INTEROP_TRACE_PARENT",
		"KC_INTEROP_TRACE_STATE",
		"KC_INTEROP_BAGGAGE",
		"KC_INTEROP_EXPECT_TRACE_ID",
		"KC_INTEROP_TLS_CA_FILE",
		"KC_INTEROP_TLS_CERT_FILE",
		"KC_INTEROP_TLS_KEY_FILE",
	}
	if forClient {
		required = append(required, "KC_INTEROP_TLS_SERVER_NAME")
	}
	for _, name := range required {
		value, err := requireEnvironment(name)
		if err != nil {
			return fixtureEnvironment{}, err
		}
		values[name] = value
	}
	serverTimeout, err := durationFromEnvironment("KC_INTEROP_SERVER_TIMEOUT_MS", 500*time.Millisecond)
	if err != nil {
		return fixtureEnvironment{}, err
	}
	clientTimeout, err := durationFromEnvironment("KC_INTEROP_CLIENT_TIMEOUT_MS", 500*time.Millisecond)
	if err != nil {
		return fixtureEnvironment{}, err
	}
	delay, err := durationFromEnvironment("KC_INTEROP_DELAY_MS", 2*time.Second)
	if err != nil {
		return fixtureEnvironment{}, err
	}
	deadline, err := durationFromEnvironment("KC_INTEROP_DEADLINE_MS", 50*time.Millisecond)
	if err != nil {
		return fixtureEnvironment{}, err
	}
	return fixtureEnvironment{
		address:       values["KC_INTEROP_ADDRESS"],
		token:         values["KC_INTEROP_EXPECT_TOKEN"],
		requestID:     values["KC_INTEROP_EXPECT_REQUEST_ID"],
		traceParent:   values["KC_INTEROP_TRACE_PARENT"],
		traceState:    values["KC_INTEROP_TRACE_STATE"],
		baggage:       values["KC_INTEROP_BAGGAGE"],
		traceID:       values["KC_INTEROP_EXPECT_TRACE_ID"],
		tlsCAFile:     values["KC_INTEROP_TLS_CA_FILE"],
		tlsCertFile:   values["KC_INTEROP_TLS_CERT_FILE"],
		tlsKeyFile:    values["KC_INTEROP_TLS_KEY_FILE"],
		tlsServerName: values["KC_INTEROP_TLS_SERVER_NAME"],
		serverTimeout: serverTimeout,
		clientTimeout: clientTimeout,
		delay:         delay,
		deadline:      deadline,
	}, nil
}

func requireEnvironment(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func durationFromEnvironment(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	milliseconds, err := strconv.ParseUint(value, 10, 64)
	const maximumMilliseconds = uint64((1<<63 - 1) / int64(time.Millisecond))
	if err != nil || milliseconds == 0 || milliseconds > maximumMilliseconds {
		return 0, fmt.Errorf("%s must be a positive millisecond duration", name)
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}
