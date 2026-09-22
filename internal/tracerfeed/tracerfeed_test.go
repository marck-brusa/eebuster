package tracerfeed

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// freeAddr asks the kernel for an unused port so tests never collide with a running testbench.
func freeAddr(t *testing.T, network string) string {
	t.Helper()
	if network == "udp" {
		conn, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve udp: %v", err)
		}
		addr := conn.LocalAddr().String()
		_ = conn.Close()
		return addr
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve tcp: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return addr
}

func TestTCPClientReceivesFrameInTracerFormat(t *testing.T) {
	feed := New()
	defer feed.Close()
	addr := freeAddr(t, "tcp")
	if err := feed.ListenTCP(addr); err != nil {
		t.Fatalf("ListenTCP: %v", err)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// The accept goroutine registers the client; publish until the line lands so the test
	// does not depend on that scheduling.
	go func() {
		for i := 0; i < 50; i++ {
			feed.Publish("send", "abc123", `{"data":[]}`)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(line, "SEND to abc123 MSG: ") {
		t.Errorf("missing direction/peer/payload separator: %q", line)
	}
	if !strings.HasSuffix(line, `{"data":[]}`+"\n") {
		t.Errorf("payload not at end of line: %q", line)
	}
	// EEBusTracer matches on a bracketed HH:MM:SS.mmm stamp; without it the line is dropped.
	if !strings.HasPrefix(line, "[") || len(strings.SplitN(line, "]", 2)[0]) != len("[15:04:05.000") {
		t.Errorf("timestamp not in the expected [HH:MM:SS.mmm] form: %q", line)
	}
}

func TestReceiveDirectionUsesFrom(t *testing.T) {
	feed := New()
	defer feed.Close()
	addr := freeAddr(t, "tcp")
	if err := feed.ListenTCP(addr); err != nil {
		t.Fatalf("ListenTCP: %v", err)
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	go func() {
		for i := 0; i < 50; i++ {
			feed.Publish("recv", "deadbeef", `{}`)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(line, "RECV from deadbeef MSG: ") {
		t.Errorf("a received frame must read RECV from: %q", line)
	}
}

func TestUDPPeerRegistersBeforeReceiving(t *testing.T) {
	feed := New()
	defer feed.Close()
	addr := freeAddr(t, "udp")
	if err := feed.ListenUDP(addr); err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}

	client, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	// EEBusTracer's --target mode announces itself with a single byte before it listens.
	if _, err := client.Write([]byte{0}); err != nil {
		t.Fatalf("register: %v", err)
	}

	go func() {
		for i := 0; i < 50; i++ {
			feed.Publish("send", "abc123", `{}`)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	buf := make([]byte, 512)
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "SEND to abc123 MSG: ") {
		t.Errorf("unexpected datagram: %q", string(buf[:n]))
	}
}

func TestPublishWithoutClientsDoesNotBlock(t *testing.T) {
	feed := New()
	defer feed.Close()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			feed.Publish("send", "abc123", `{}`)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Publish blocked with no tracer attached")
	}
}

func TestSlowClientIsDroppedNotBlocking(t *testing.T) {
	feed := New()
	defer feed.Close()
	addr := freeAddr(t, "tcp")
	if err := feed.ListenTCP(addr); err != nil {
		t.Fatalf("ListenTCP: %v", err)
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond) // let the accept loop register it

	// Far more than queueDepth, with nobody reading: capture must not stall.
	done := make(chan struct{})
	go func() {
		for i := 0; i < queueDepth*10; i++ {
			feed.Publish("send", "abc123", strings.Repeat("x", 512))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a client that stopped reading")
	}
}
