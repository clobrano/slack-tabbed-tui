package ws

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// echo serves a WebSocket that echoes text messages, checking the
// handshake header, and closes with 4000 on "bye".
func echo(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "d=xoxd-1" {
			http.Error(w, "no cookie", http.StatusUnauthorized)
			return
		}
		c, err := Accept(w, r)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			op, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			if string(msg) == "bye" {
				_ = c.writeFrame(OpClose, append([]byte{0x0f, 0xa0}, "done"...))
				return
			}
			if err := c.writeFrame(op, msg); err != nil {
				return
			}
		}
	}))
}

func wsURL(s *httptest.Server) string { return "ws" + strings.TrimPrefix(s.URL, "http") }

func TestEchoAndClose(t *testing.T) {
	s := echo(t)
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, wsURL(s)+"/?x=1", http.Header{"Cookie": {"d=xoxd-1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{0, 5, 125, 126, 70000} {
		msg := strings.Repeat("x", size)
		if err := c.WriteText([]byte(msg)); err != nil {
			t.Fatal(err)
		}
		op, got, err := c.ReadMessage()
		if err != nil || op != OpText || string(got) != msg {
			t.Fatalf("size %d: op %d, %d bytes, %v", size, op, len(got), err)
		}
	}
	if err := c.WriteJSON(map[string]int{"id": 1}); err != nil {
		t.Fatal(err)
	}
	if _, got, _ := c.ReadMessage(); string(got) != `{"id":1}` {
		t.Errorf("json echo %q", got)
	}
	if err := c.Ping(); err != nil {
		t.Fatal(err)
	}
	_ = c.WriteText([]byte("bye"))
	_, _, err = c.ReadMessage()
	var ce *CloseError
	if !errors.As(err, &ce) || ce.Code != 4000 || ce.Reason != "done" {
		t.Errorf("close: %v", err)
	}
}

func TestHandshakeRejected(t *testing.T) {
	s := echo(t)
	defer s.Close()
	_, err := Dial(context.Background(), wsURL(s), nil)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v", err)
	}
	if _, err := Dial(context.Background(), "http://x", nil); err == nil {
		t.Error("http scheme accepted")
	}
}

// rawServer runs fn on the server side of a handshaken connection.
func rawServer(t *testing.T, fn func(c *Conn)) *Conn {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Accept(w, r)
		if err != nil {
			return
		}
		fn(c)
	}))
	t.Cleanup(s.Close)
	c, err := Dial(context.Background(), wsURL(s), nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFragmentsAndPing(t *testing.T) {
	pong := make(chan string, 1)
	c := rawServer(t, func(s *Conn) {
		// "hel" + ping + "lo", as three frames.
		_, _ = s.nc.Write([]byte{0x01, 3, 'h', 'e', 'l'})
		_, _ = s.nc.Write([]byte{0x89, 2, 'p', '1'})
		_, _ = s.nc.Write([]byte{0x80, 2, 'l', 'o'})
		_, p, err := s.readFrameOp(OpPong)
		if err == nil {
			pong <- string(p)
		}
	})
	op, msg, err := c.ReadMessage()
	if err != nil || op != OpText || string(msg) != "hello" {
		t.Fatalf("got %d %q %v", op, msg, err)
	}
	select {
	case p := <-pong:
		if p != "p1" {
			t.Errorf("pong payload %q", p)
		}
	case <-time.After(2 * time.Second):
		t.Error("no pong")
	}
}

// readFrameOp reads frames until one with op.
func (c *Conn) readFrameOp(op int) (bool, []byte, error) {
	for {
		fin, got, p, err := c.readFrame()
		if err != nil || got == op {
			return fin, p, err
		}
	}
}

func TestBadFrames(t *testing.T) {
	for name, frame := range map[string][]byte{
		"orphan continuation": {0x80, 1, 'x'},
		"long control":        append([]byte{0x89, 126, 0, 200}, make([]byte, 200)...),
		"unknown opcode":      {0x83, 0},
	} {
		c := rawServer(t, func(s *Conn) { _, _ = s.nc.Write(frame) })
		if _, _, err := c.ReadMessage(); err == nil || errors.Is(err, io.EOF) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestProxyConnect(t *testing.T) {
	// A CONNECT proxy in front of a TLS echo server.
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Accept(w, r)
		if err != nil {
			return
		}
		defer c.Close()
		op, msg, err := c.ReadMessage()
		if err == nil {
			_ = c.writeFrame(op, msg)
		}
	}))
	defer backend.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	connects := make(chan string, 1)
	go func() {
		for {
			pc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer pc.Close()
				req, err := http.ReadRequest(bufio.NewReader(pc))
				if err != nil {
					return
				}
				connects <- req.Method + " " + req.Host
				bc, err := net.Dial("tcp", req.Host)
				if err != nil {
					return
				}
				defer bc.Close()
				_, _ = io.WriteString(pc, "HTTP/1.1 200 OK\r\n\r\n")
				go io.Copy(bc, pc)
				_, _ = io.Copy(pc, bc)
			}()
		}
	}()
	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	old := Proxy
	Proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }
	defer func() { Proxy = old }()

	// The test server's certificate is for 127.0.0.1 but not trusted:
	// check the tunnel by observing the CONNECT and the TLS failure.
	host := strings.TrimPrefix(backend.URL, "https://")
	_, err = Dial(context.Background(), "wss://"+host, nil)
	select {
	case got := <-connects:
		if got != "CONNECT "+host {
			t.Errorf("proxy saw %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy not used")
	}
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("expected a certificate error through the tunnel, got %v", err)
	}
}
