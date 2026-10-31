package traffic

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing/common/logger"
)

type countingCloser struct{ closes atomic.Int32 }

func (c *countingCloser) Close() error {
	c.closes.Add(1)
	return nil
}

func TestCloseOnPanicEndsTheConnectionAndNotTheProcess(t *testing.T) {
	conn := &countingCloser{}
	var closedWith error
	func() {
		defer CloseOnPanic(context.Background(), logger.NOP(), conn, func(err error) { closedWith = err })
		panic("hostile input")
	}()
	if conn.closes.Load() != 1 {
		t.Errorf("closed %d times, want once", conn.closes.Load())
	}
	if closedWith == nil {
		t.Error("onClose was not told")
	}
}

func TestCloseOnPanicLeavesANormalReturnAlone(t *testing.T) {
	conn := &countingCloser{}
	called := false
	func() {
		defer CloseOnPanic(context.Background(), logger.NOP(), conn, func(error) { called = true })
	}()
	if conn.closes.Load() != 0 || called {
		t.Error("a connection that didn't panic was closed")
	}
}

func TestCloseOnPanicWithNoConnection(t *testing.T) {
	func() {
		defer CloseOnPanic(context.Background(), logger.NOP(), nil, nil)
		panic(errors.New("hostile packet"))
	}()
}
