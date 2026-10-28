package nodecore

import (
	"context"
	"github.com/legendary1205/rapido-go/internal/nodecore/traffic"
	"github.com/sagernet/sing-box/option"
	"net"
	"strconv"
	"testing"
)

func TestRemovingUserClosesEstablishedConnection(t *testing.T) {
	const uuid = "8f8a4c1e-1e2a-4b8a-9b1a-00000000beef"
	dest := startEchoServer(t)
	host, p, err := net.SplitHostPort(dest)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	listenPort := freePort(t)
	node, err := New(context.Background(), buildTestOptions(listenPort, []option.VLESSUser{{Name: "audituser", UUID: uuid}}), traffic.NewManager())
	if err != nil {
		t.Fatal(err)
	}
	if err := node.Start(); err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	conn := dialVLESS(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort)), uuid, host, port)
	defer conn.Close()
	if err := echoRoundTrip(t, conn, "before-removal"); err != nil {
		t.Fatal(err)
	}
	if err := node.UpdateUsers("vless-in", "vless", nil); err != nil {
		t.Fatal(err)
	}
	if err := echoRoundTrip(t, conn, "after-removal"); err == nil {
		t.Fatal("removed user retained an established stream")
	}
}
