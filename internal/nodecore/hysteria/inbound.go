// Package hysteria is a minimal fork of sing-box's protocol/hysteria
// inbound (github.com/sagernet/sing-box@v1.14.0, protocol/hysteria/
// inbound.go - Hysteria v1, not to be confused with internal/nodecore/
// hysteria2's own separate fork of the v2 protocol), copied verbatim except
// for the same additions every other fork in this package makes:
// UpdateUsers, an exported method that calls the same underlying
// sing-quic/hysteria.Service[U].UpdateUsers the original constructor
// already calls internally; per-user byte counting and connection tracking
// on every accepted connection; and userkey.Key user identities in place of
// sing-box's own plain int index (see vless's own doc comment for the
// hot-update race an index has that a Key does not - sing-quic's
// Service[U] is generic over a comparable U, so *userkey.Key drops in
// exactly like it does for hysteria2/tuic, keyed here by auth string rather
// than by index). buildInboundQUICOptions is copied from upstream's own
// unexported protocol/hysteria/quic.go verbatim, since it isn't exported.
//
// Re-sync this file against sing-box's own protocol/hysteria/inbound.go on
// every sing-box upgrade; a diff is the fastest way to catch upstream
// changes this fork needs to absorb.
package hysteria

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	qtls "github.com/sagernet/sing-quic"
	"github.com/sagernet/sing-quic/hysteria"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/auth"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/legendary1205/rapido-go/internal/nodecore/traffic"
	"github.com/legendary1205/rapido-go/internal/nodecore/userkey"
)

func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[option.HysteriaInboundOptions](registry, C.TypeHysteria, NewInbound)
}

type Inbound struct {
	inbound.Adapter
	router     adapter.Router
	logger     log.ContextLogger
	listener   *listener.Listener
	tlsConfig  tls.ServerConfig
	service    *hysteria.Service[*userkey.Key]
	trafficMgr *traffic.Manager
	conns      *traffic.ConnGroup
}

// buildInboundQUICOptions mirrors upstream's own unexported function of the
// same name in protocol/hysteria/quic.go byte for byte.
func buildInboundQUICOptions(options option.HysteriaInboundOptions) qtls.QUICOptions {
	quicOptions := qtls.QUICOptions{
		IdleTimeout:             options.IdleTimeout.Build(),
		KeepAlivePeriod:         options.KeepAlivePeriod.Build(),
		StreamReceiveWindow:     options.StreamReceiveWindow.Value(),
		ConnectionReceiveWindow: options.ConnectionReceiveWindow.Value(),
		MaxConcurrentStreams:    options.MaxConcurrentStreams,
		InitialPacketSize:       options.InitialPacketSize,
		DisablePathMTUDiscovery: options.DisablePathMTUDiscovery,
	}
	if quicOptions.ConnectionReceiveWindow == 0 {
		quicOptions.ConnectionReceiveWindow = options.ReceiveWindowConn //nolint:staticcheck
	}
	if quicOptions.StreamReceiveWindow == 0 {
		quicOptions.StreamReceiveWindow = options.ReceiveWindowClient //nolint:staticcheck
	}
	if quicOptions.MaxConcurrentStreams == 0 {
		quicOptions.MaxConcurrentStreams = options.MaxConnClient //nolint:staticcheck
	}
	if !quicOptions.DisablePathMTUDiscovery {
		quicOptions.DisablePathMTUDiscovery = options.DisableMTUDiscovery //nolint:staticcheck
	}
	return quicOptions
}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.HysteriaInboundOptions) (adapter.Inbound, error) {
	options.UDPFragmentDefault = true
	if options.TLS == nil || !options.TLS.Enabled {
		return nil, C.ErrTLSRequired
	}
	tlsConfig, err := tls.NewServer(ctx, logger, common.PtrValueOrDefault(options.TLS))
	if err != nil {
		return nil, err
	}
	inb := &Inbound{
		Adapter: inbound.NewAdapter(C.TypeHysteria, tag),
		router:  router,
		logger:  logger,
		listener: listener.New(listener.Options{
			Context: ctx,
			Logger:  logger,
			Listen:  options.ListenOptions,
		}),
		tlsConfig:  tlsConfig,
		trafficMgr: traffic.FromContext(ctx),
		conns:      traffic.ConnGroupFromContext(ctx),
	}
	var sendBps, receiveBps uint64
	if options.Up.Value() > 0 {
		sendBps = options.Up.Value()
	} else {
		sendBps = uint64(options.UpMbps) * hysteria.MbpsToBps
	}
	if options.Down.Value() > 0 {
		receiveBps = options.Down.Value()
	} else {
		receiveBps = uint64(options.DownMbps) * hysteria.MbpsToBps
	}
	var udpTimeout time.Duration
	if options.UDPTimeout != 0 {
		udpTimeout = time.Duration(options.UDPTimeout)
	} else {
		udpTimeout = C.UDPTimeout
	}
	service, err := hysteria.NewService[*userkey.Key](hysteria.ServiceOptions{
		Context:       ctx,
		Logger:        logger,
		SendBPS:       sendBps,
		ReceiveBPS:    receiveBps,
		XPlusPassword: options.Obfs,
		TLSConfig:     tlsConfig,
		QUICOptions:   buildInboundQUICOptions(options),
		UDPTimeout:    udpTimeout,
		Handler:       inb,
	})
	if err != nil {
		return nil, err
	}
	inb.service = service
	inb.UpdateUsers(options.Users)
	return inb, nil
}

// UpdateUsers replaces the inbound's user list in place, on the already-
// running QUIC listener - the addition this fork exists for. Keyed by the
// resolved auth string (sing-quic's own auth lookup), not index - see this
// package's own doc comment.
func (h *Inbound) UpdateUsers(users []option.HysteriaUser) {
	h.service.UpdateUsers(
		userkey.Build(common.Map(users, func(it option.HysteriaUser) string { return it.Name })),
		common.Map(users, func(it option.HysteriaUser) string {
			if it.AuthString != "" {
				return it.AuthString
			}
			return string(it.Auth)
		}),
	)
}

func (h *Inbound) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	ctx = log.ContextWithNewID(ctx)
	var metadata adapter.InboundContext
	metadata.Inbound = h.Tag()
	metadata.InboundType = h.Type()
	//nolint:staticcheck
	metadata.InboundDetour = h.listener.ListenOptions().Detour
	//nolint:staticcheck
	metadata.OriginDestination = h.listener.UDPAddr()
	metadata.Source = source
	metadata.Destination = destination
	key, loaded := auth.UserFromContext[*userkey.Key](ctx)
	if !loaded {
		N.CloseOnHandshakeFailure(conn, onClose, os.ErrInvalid)
		return
	}
	user := key.Label
	if key.Name != "" {
		metadata.User = key.Name
	}
	h.logger.InfoContext(ctx, "[", user, "] inbound connection to ", metadata.Destination)
	if h.trafficMgr != nil {
		onClose = h.trafficMgr.TrackClose(user, conn, h.listener.ListenOptions().ListenPort, onClose)
		conn = traffic.WrapConn(conn, user, h.trafficMgr)
	}
	onClose = h.conns.Track(conn, onClose)
	h.router.RouteConnectionEx(ctx, conn, metadata, onClose)
}

func (h *Inbound) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	ctx = log.ContextWithNewID(ctx)
	var metadata adapter.InboundContext
	metadata.Inbound = h.Tag()
	metadata.InboundType = h.Type()
	//nolint:staticcheck
	metadata.InboundDetour = h.listener.ListenOptions().Detour
	//nolint:staticcheck
	metadata.OriginDestination = h.listener.UDPAddr()
	metadata.Source = source
	metadata.Destination = destination
	key, loaded := auth.UserFromContext[*userkey.Key](ctx)
	if !loaded {
		N.CloseOnHandshakeFailure(conn, onClose, os.ErrInvalid)
		return
	}
	user := key.Label
	if key.Name != "" {
		metadata.User = key.Name
	}
	h.logger.InfoContext(ctx, "[", user, "] inbound packet connection to ", metadata.Destination)
	onClose = h.conns.Track(conn, onClose)
	if h.trafficMgr != nil {
		conn = traffic.WrapPacketConn(conn, user, h.trafficMgr)
	}
	h.router.RoutePacketConnectionEx(ctx, conn, metadata, onClose)
}

func (h *Inbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	if h.tlsConfig != nil {
		err := h.tlsConfig.Start()
		if err != nil {
			return err
		}
	}
	packetConn, err := h.listener.ListenUDP()
	if err != nil {
		return err
	}
	return h.service.Start(packetConn)
}

func (h *Inbound) Close() error {
	return common.Close(
		h.listener,
		h.tlsConfig,
		common.PtrOrNil(h.service),
	)
}
