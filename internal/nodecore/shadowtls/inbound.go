// Package shadowtls pairs ShadowTLS v3 (a fork of sing-box's protocol/
// shadowtls inbound, github.com/sagernet/sing-box@v1.14.0) with an embedded
// shadowsocks-2022 inner data layer, combined into one inbound this panel
// exposes as a single protocol on a single port.
//
// Why combined: ShadowTLS on its own is a disguise/authentication layer
// with no concept of "where to send the client's traffic" at all - a
// connection it hands to sing-box's router carries no meaningful
// Destination unless something upstream of it parsed one from the wire.
// Every real sing-box deployment chains it to a second inbound via
// ListenOptions.Detour/metadata.InboundDetour, almost always a
// shadowsocks-2022 server. This fork inlines that second inbound directly
// (an embedded shadowaead_2022.Service, fed in-process from the ShadowTLS
// handshake's own completion callback) instead of making the admin create
// and wire two separate inbounds for one usable proxy - one port, one
// protocol choice, zero admin-visible plumbing, matching every other
// protocol's one-inbound model in this codebase.
//
// The inner layer is single-key, not per-user: the outer ShadowTLS
// handshake already distinguishes users by password (see
// third_party/sing-shadowtls's own patched hot-update), so the inner
// shadowsocks-2022 service only needs to decrypt, not re-authenticate -
// one method+PSK shared by the whole inbound (EffectiveInbound's
// ShadowTLSInnerMethod/ShadowTLSInnerPassword), the same inbound-level-
// secret split Snell's own PSK already established. The outer
// ShadowTLS-authenticated username survives in ctx (auth.ContextWithUser,
// set by third_party/sing-shadowtls's own v3 handshake) all the way
// through the inner service's NewConnection call unchanged, so traffic
// accounting below still attributes bytes to the right user despite the
// inner layer itself being user-agnostic.
//
// Hot-swap: only the outer per-user password list can change on a running
// listener (UpdateUsers, via the patched third_party/sing-shadowtls
// service - see that package's own doc comment). The inner method+PSK is
// an inbound-level secret fixed at creation, same as Snell's PSK; changing
// it needs a listener rebuild, which this panel's node-agent already does
// for any inbound-level (non-user-list) field change.
//
// Re-sync against sing-box's own protocol/shadowtls/inbound.go and
// protocol/shadowsocks/inbound_multi.go, and third_party/sing-shadowtls,
// on every sing-box upgrade.
package shadowtls

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/common/mux"
	"github.com/sagernet/sing-box/common/uot"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-shadowsocks"
	"github.com/sagernet/sing-shadowsocks/shadowaead_2022"
	shadowtls "github.com/sagernet/sing-shadowtls"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/auth"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/legendary1205/rapido-go/internal/nodecore/traffic"
)

func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[InboundOptions](registry, C.TypeShadowTLS, NewInbound)
}

// InboundOptions is this fork's own options shape, not sing-box's
// option.ShadowTLSInboundOptions - box.go's own inbound construction loop
// hands inboundOptions.Options straight to the registry with no
// serialization step, so cmd/node/main.go can construct this directly and
// there is no need to shoehorn the inner shadowsocks-2022 method/password
// into sing-box's own upstream struct.
type InboundOptions struct {
	option.ListenOptions
	Users         []option.ShadowTLSUser
	InnerMethod   string
	InnerPassword string
}

type Inbound struct {
	inbound.Adapter
	router     adapter.ConnectionRouterEx
	logger     logger.ContextLogger
	listener   *listener.Listener
	service    *shadowtls.Service
	inner      shadowsocks.Service
	trafficMgr *traffic.Manager
	conns      *traffic.ConnGroup
}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options InboundOptions) (adapter.Inbound, error) {
	if len(options.Users) == 0 {
		return nil, E.New("missing users")
	}
	if options.InnerMethod == "" || options.InnerPassword == "" {
		return nil, E.New("missing inner shadowsocks-2022 method/password")
	}

	// WildcardSNI "all" needs a dialer capable of reaching an arbitrary
	// domain (the client's own SNI) - a plain system dialer, since this
	// panel has no admin-facing "handshake server" setting for v3 at all.
	handshakeDialer, err := dialer.New(ctx, option.DialerOptions{}, true)
	if err != nil {
		return nil, err
	}
	return newInbound(ctx, router, logger, tag, options,
		shadowtls.WildcardSNI(option.ShadowTLSWildcardSNIAll), shadowtls.HandshakeConfig{Dialer: handshakeDialer})
}

// newInbound is NewInbound's real body, with the outer ShadowTLS service's
// handshake-relay target factored out as parameters - production always
// goes through NewInbound's own WildcardSNI=all (see that function's doc
// comment), but this fork's own tests need a fixed, local
// Handshake.Server instead: WildcardSNI=all hardcodes the relay target to
// "<client's claimed SNI>:443" (third_party/sing-shadowtls's own
// NewConnection, v3 case), which needs a real reachable TLS server on
// port 443 for whatever name the test picks - impractical in a portable,
// unprivileged unit test. WildcardSNIOff uses Handshake.Server exactly as
// given instead, letting a test point it at a throwaway local TLS server
// on an ephemeral port while exercising the exact same inner+outer gluing
// logic (newConnection/NewConnectionEx below) production uses.
func newInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options InboundOptions, wildcardSNI shadowtls.WildcardSNI, handshake shadowtls.HandshakeConfig) (adapter.Inbound, error) {
	inb := &Inbound{
		Adapter:    inbound.NewAdapter(C.TypeShadowTLS, tag),
		logger:     logger,
		trafficMgr: traffic.FromContext(ctx),
		conns:      traffic.ConnGroupFromContext(ctx),
	}
	var err error
	inb.router, err = mux.NewRouterWithOptions(uot.NewRouter(router, logger), logger, option.InboundMultiplexOptions{})
	if err != nil {
		return nil, err
	}

	inb.inner, err = shadowaead_2022.NewServiceWithPassword(
		options.InnerMethod, options.InnerPassword, int64(C.UDPTimeout.Seconds()),
		adapter.NewLegacyUpstreamHandler(adapter.InboundContext{}, inb.newConnection, inb.newPacketConnection, inb),
		time.Now,
	)
	if err != nil {
		return nil, E.Cause(err, "inner shadowsocks-2022 layer")
	}

	service, err := shadowtls.NewService(shadowtls.ServiceConfig{
		Version: 3,
		Users: common.Map(options.Users, func(it option.ShadowTLSUser) shadowtls.User {
			return shadowtls.User(it)
		}),
		Handshake:   handshake,
		WildcardSNI: wildcardSNI,
		Handler:     (*inboundHandler)(inb),
		Logger:      logger,
	})
	if err != nil {
		return nil, err
	}
	inb.service = service
	inb.listener = listener.New(listener.Options{
		Context:           ctx,
		Logger:            logger,
		Network:           []string{N.NetworkTCP},
		Listen:            options.ListenOptions,
		ConnectionHandler: inb,
	})
	return inb, nil
}

// UpdateUsers replaces the outer ShadowTLS per-user password list on the
// already-running listener - see this package's own doc comment for why
// the inner shadowsocks-2022 layer never needs (or gets) an equivalent.
func (h *Inbound) UpdateUsers(users []option.ShadowTLSUser) {
	h.service.UpdateUsers(common.Map(users, func(it option.ShadowTLSUser) shadowtls.User {
		return shadowtls.User(it)
	}))
}

func (h *Inbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	return h.listener.Start()
}

func (h *Inbound) Close() error {
	return h.listener.Close()
}

// NewError satisfies the inner shadowsocks-2022 service's own error-handler
// requirement (the fourth arg to adapter.NewLegacyUpstreamHandler in
// NewInbound) - distinct from inboundHandler.NewError below, which instead
// answers the outer ShadowTLS service's own error path.
func (h *Inbound) NewError(ctx context.Context, err error) {
	common.Close(err)
	if E.IsClosedOrCanceled(err) {
		h.logger.DebugContext(ctx, "connection closed: ", err)
		return
	}
	h.logger.ErrorContext(ctx, err)
}

func (h *Inbound) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	err := h.service.NewConnection(adapter.WithContext(log.ContextWithNewID(ctx), &metadata), conn, metadata.Source, metadata.Destination, onClose)
	N.CloseOnHandshakeFailure(conn, onClose, err)
	if err != nil {
		if E.IsClosedOrCanceled(err) {
			h.logger.DebugContext(ctx, "connection closed: ", err)
		} else {
			h.logger.ErrorContext(ctx, E.Cause(err, "process connection from ", metadata.Source))
		}
	}
}

// inboundHandler receives the connection once the OUTER ShadowTLS
// handshake finishes - still carrying no real destination (see this
// package's own doc comment), so instead of routing it directly (what the
// equivalent step in a bare ShadowTLS inbound would do), it hands the
// stream to the embedded inner shadowsocks-2022 service, which decrypts
// its own header, learns the real destination, and only then calls back
// into h.newConnection to route it.
type inboundHandler Inbound

func (h *inboundHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	err := h.inner.NewConnection(ctx, conn, M.Metadata{Source: source})
	N.CloseOnHandshakeFailure(conn, onClose, err)
	if err != nil {
		if E.IsClosedOrCanceled(err) {
			h.logger.DebugContext(ctx, "connection closed: ", err)
		} else {
			h.logger.ErrorContext(ctx, E.Cause(err, "process connection from ", source))
		}
	}
}

func (h *inboundHandler) NewError(ctx context.Context, err error) {
	if E.IsClosedOrCanceled(err) {
		h.logger.DebugContext(ctx, "connection closed: ", err)
		return
	}
	h.logger.ErrorContext(ctx, err)
}

// newConnection is the inner shadowsocks-2022 service's own callback,
// invoked once it has decrypted its header and resolved metadata.
// Destination for real - the first point in the whole chain where routing
// is actually possible. userName comes from the OUTER ShadowTLS layer's
// own auth.ContextWithUser call (see this package's own doc comment on
// why ctx, not this inner layer, carries user identity).
func (h *Inbound) newConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext) error {
	userName, _ := auth.UserFromContext[string](ctx)
	if userName != "" {
		metadata.User = userName
	}
	h.logger.InfoContext(ctx, "[", userName, "] inbound connection to ", metadata.Destination)
	metadata.Inbound = h.Tag()
	metadata.InboundType = h.Type()
	//nolint:staticcheck
	metadata.InboundDetour = h.listener.ListenOptions().Detour
	// Closing the core closes every connection it accepted; see traffic.ConnGroup.
	defer h.conns.Hold(conn)()
	if h.trafficMgr != nil && userName != "" {
		defer h.trafficMgr.OpenConn(userName, traffic.LocalPort(conn, h.listener.ListenOptions().ListenPort))()
		conn = traffic.WrapConn(conn, userName, h.trafficMgr)
	}
	return h.router.RouteConnection(ctx, conn, metadata)
}

// newPacketConnection is structurally unreachable: this fork's listener is
// TCP-only (ShadowTLS carries no UDP transport of its own), and the inner
// shadowsocks-2022 service's own UDP-over-TCP support (uot.NewRouter,
// wrapping h.router above) operates entirely at the router/metadata level
// - it never calls back into the Service's own NewPacket/this callback.
// Still required to satisfy shadowsocks.Handler's interface.
func (h *Inbound) newPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext) error {
	return os.ErrInvalid
}
