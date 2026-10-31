package traffic

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"

	"github.com/sagernet/sing/common/logger"
	N "github.com/sagernet/sing/common/network"
)

// CloseOnPanic is deferred at the top of every inbound's connection entry
// point. sing-box runs those on a goroutine of their own with no recover, so a
// panic while parsing what one client sent would otherwise end the whole node
// process and drop every other customer on it. The panicking connection is
// closed and logged instead. conn is nil on a path that has no connection of its
// own, like a Shadowsocks UDP packet.
//
// This is the backstop, not the fix: each input known to panic is also refused
// where it is parsed, since a read on a connection's copy goroutine is out of
// reach of this, and an out-of-memory failure can't be recovered at all.
func CloseOnPanic(ctx context.Context, log logger.ContextLogger, conn io.Closer, onClose N.CloseHandlerFunc) {
	recovered := recover()
	if recovered == nil {
		return
	}
	log.ErrorContext(ctx, "recovered from a panic handling a connection: ", recovered, "\n", string(debug.Stack()))
	if conn != nil {
		_ = N.CloseOnHandshakeFailure(conn, onClose, fmt.Errorf("panic: %v", recovered))
	}
}
