// Package naive is a minimal fork of sing-box's protocol/naive inbound
// (github.com/sagernet/sing-box@v1.14.0, protocol/naive/inbound.go),
// TCP-only (see NewInbound's own comment on why UDP/HTTP3 is rejected
// outright rather than silently ignored) with one structural change beyond
// what every other fork in this package makes: naive's own auth check
// already hands this code the plain username string directly (HTTP Basic
// Auth, parsed inline in ServeHTTP) - there is no generic per-connection
// "user key in context" step the way the QUIC/TCP-family forks rely on, so
// there is no userkey.Key here at all, just the username used as-is for
// traffic tracking.
//
// The other difference: sing.common.auth.Authenticator (what upstream's own
// NewInbound builds once and never touches again) has no mutator at all -
// not generic over a key type with its own UpdateUsers the way sing-quic's
// Service[U] is, just a plain built-once value. UpdateUsers here instead
// builds a brand new Authenticator and atomically swaps the pointer
// ServeHTTP reads, which is the closest equivalent: every new incoming
// CONNECT request sees the latest user list, with nothing to restart and no
// cross-goroutine lock contention on the hot path.
//
// Re-sync this file and inbound_conn.go against sing-box's own
// protocol/naive on every sing-box upgrade; a diff is the fastest way to
// catch upstream changes this fork needs to absorb.
package naive

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/common/uot"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/v2rayhttp"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/auth"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	aTLS "github.com/sagernet/sing/common/tls"
	sHttp "github.com/sagernet/sing/protocol/http"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c" //nolint:staticcheck

	"github.com/legendary1205/rapido-go/internal/nodecore/traffic"
)

func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[option.NaiveInboundOptions](registry, C.TypeNaive, NewInbound)
}

type Inbound struct {
	inbound.Adapter
	ctx           context.Context
	router        adapter.ConnectionRouterEx
	logger        logger.ContextLogger
	listener      *listener.Listener
	authenticator atomic.Pointer[auth.Authenticator]
	tlsConfig     tls.ServerConfig
	httpServer    *http.Server
	trafficMgr    *traffic.Manager
	conns         *traffic.ConnGroup
}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.NaiveInboundOptions) (adapter.Inbound, error) {
	if len(options.Users) == 0 {
		return nil, E.New("missing users")
	}
	if common.Contains(options.Network.Build(), N.NetworkUDP) {
		return nil, E.New("naive: UDP/HTTP3 is not supported by this inbound - remove \"udp\" from network")
	}
	inb := &Inbound{
		Adapter: inbound.NewAdapter(C.TypeNaive, tag),
		ctx:     ctx,
		router:  uot.NewRouter(router, logger),
		logger:  logger,
		listener: listener.New(listener.Options{
			Context: ctx,
			Logger:  logger,
			Listen:  options.ListenOptions,
		}),
		trafficMgr: traffic.FromContext(ctx),
		conns:      traffic.ConnGroupFromContext(ctx),
	}
	inb.authenticator.Store(auth.NewAuthenticator(options.Users))
	if options.TLS != nil {
		tlsConfig, err := tls.NewServer(ctx, logger, common.PtrValueOrDefault(options.TLS))
		if err != nil {
			return nil, err
		}
		inb.tlsConfig = tlsConfig
	}
	return inb, nil
}

// UpdateUsers replaces the inbound's user list in place, on the already-
// running HTTP listener - see this package's own doc comment for why this
// is a fresh Authenticator swapped atomically rather than a call into an
// upstream mutator (there isn't one).
func (n *Inbound) UpdateUsers(users []auth.User) {
	n.authenticator.Store(auth.NewAuthenticator(users))
}

func (n *Inbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	if n.tlsConfig != nil {
		if err := n.tlsConfig.Start(); err != nil {
			return E.Cause(err, "create TLS config")
		}
	}
	tcpListener, err := n.listener.ListenTCP()
	if err != nil {
		return err
	}
	n.httpServer = &http.Server{
		//nolint:staticcheck
		Handler: h2c.NewHandler(n, &http2.Server{}),
		BaseContext: func(net.Listener) context.Context {
			return n.ctx
		},
	}
	ln := net.Listener(tcpListener)
	if n.tlsConfig != nil {
		if len(n.tlsConfig.NextProtos()) == 0 {
			n.tlsConfig.SetNextProtos([]string{http2.NextProtoTLS, "http/1.1"})
		} else if !common.Contains(n.tlsConfig.NextProtos(), http2.NextProtoTLS) {
			n.tlsConfig.SetNextProtos(append([]string{http2.NextProtoTLS}, n.tlsConfig.NextProtos()...))
		}
		ln = aTLS.NewListener(tcpListener, n.tlsConfig)
	}
	go func() {
		if sErr := n.httpServer.Serve(ln); sErr != nil && !errors.Is(sErr, http.ErrServerClosed) {
			n.logger.Error("http server serve error: ", sErr)
		}
	}()
	return nil
}

func (n *Inbound) Close() error {
	return common.Close(
		n.listener,
		common.PtrOrNil(n.httpServer),
		n.tlsConfig,
	)
}

func (n *Inbound) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	ctx := log.ContextWithNewID(request.Context())
	if request.Method != "CONNECT" {
		rejectHTTP(writer, http.StatusBadRequest)
		return
	} else if request.Header.Get("Padding") == "" {
		rejectHTTP(writer, http.StatusBadRequest)
		return
	}
	userName, password, authOk := sHttp.ParseBasicAuth(request.Header.Get("Proxy-Authorization"))
	if authOk {
		if authenticator := n.authenticator.Load(); authenticator != nil {
			authOk = authenticator.Verify(userName, password)
		} else {
			authOk = false
		}
	}
	if !authOk {
		rejectHTTP(writer, http.StatusProxyAuthRequired)
		return
	}
	writer.Header().Set("Padding", generatePaddingHeader())
	writer.WriteHeader(http.StatusOK)
	writer.(http.Flusher).Flush()

	hostPort := request.Header.Get("-connect-authority")
	if hostPort == "" {
		hostPort = request.URL.Host
		if hostPort == "" {
			hostPort = request.Host
		}
	}
	source := sHttp.SourceAddress(request)
	destination := M.ParseSocksaddr(hostPort).Unwrap()

	if hijacker, isHijacker := writer.(http.Hijacker); isHijacker {
		conn, _, err := hijacker.Hijack()
		if err != nil {
			return
		}
		n.newConnection(ctx, false, &naiveConn{Conn: conn}, userName, source, destination)
	} else {
		n.newConnection(ctx, true, &naiveH2Conn{
			reader:        request.Body,
			writer:        writer,
			flusher:       writer.(http.Flusher),
			remoteAddress: source,
		}, userName, source, destination)
	}
}

func (n *Inbound) newConnection(ctx context.Context, waitForClose bool, conn net.Conn, userName string, source M.Socksaddr, destination M.Socksaddr) {
	if userName != "" {
		n.logger.InfoContext(ctx, "[", userName, "] inbound connection to ", destination)
	} else {
		n.logger.InfoContext(ctx, "inbound connection to ", destination)
	}
	var metadata adapter.InboundContext
	metadata.Inbound = n.Tag()
	metadata.InboundType = n.Type()
	//nolint:staticcheck
	metadata.InboundDetour = n.listener.ListenOptions().Detour
	metadata.Source = source
	metadata.Destination = destination
	metadata.OriginDestination = M.SocksaddrFromNet(conn.LocalAddr()).Unwrap()
	metadata.User = userName

	var onClose N.CloseHandlerFunc
	if n.trafficMgr != nil && userName != "" {
		onClose = n.trafficMgr.TrackClose(userName, conn, n.listener.ListenOptions().ListenPort, nil)
		conn = traffic.WrapConn(conn, userName, n.trafficMgr)
	}
	onClose = n.conns.Track(conn, onClose)

	if !waitForClose {
		n.router.RouteConnectionEx(ctx, conn, metadata, onClose)
	} else {
		done := make(chan struct{})
		wrapper := v2rayhttp.NewHTTP2Wrapper(conn)
		n.router.RouteConnectionEx(ctx, wrapper, metadata, N.OnceClose(func(it error) {
			if onClose != nil {
				onClose(it)
			}
			close(done)
		}))
		<-done
		wrapper.CloseWrapper()
	}
}

func rejectHTTP(writer http.ResponseWriter, statusCode int) {
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		writer.WriteHeader(statusCode)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		writer.WriteHeader(statusCode)
		return
	}
	if tcpConn, isTCP := common.Cast[*net.TCPConn](conn); isTCP {
		tcpConn.SetLinger(0)
	}
	conn.Close()
}
