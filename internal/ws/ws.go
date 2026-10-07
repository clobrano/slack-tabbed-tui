// Package ws is a small WebSocket (RFC 6455) implementation: a client
// for Slack's event stream and the Chrome DevTools protocol, and a
// server side for test fakes. Messages are read whole; control frames
// (ping, pong, close) are handled inside ReadMessage.
package ws

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Opcodes.
const (
	OpContinuation = 0x0
	OpText         = 0x1
	OpBinary       = 0x2
	OpClose        = 0x8
	OpPing         = 0x9
	OpPong         = 0xA
)

// MaxMessage is the largest message ReadMessage accepts.
const MaxMessage = 32 << 20

const guid = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// CloseError is returned by ReadMessage when the peer closed the
// connection.
type CloseError struct {
	Code   int
	Reason string
}

func (e *CloseError) Error() string { return fmt.Sprintf("websocket closed: %d %s", e.Code, e.Reason) }

// Conn is a WebSocket connection. Reads must come from one goroutine;
// writes are safe from many.
type Conn struct {
	nc     net.Conn
	br     *bufio.Reader
	client bool // clients mask their frames

	wmu    sync.Mutex
	closed bool
}

// Dial opens a WebSocket connection to a ws:// or wss:// URL, sending
// header with the handshake (e.g. a Cookie). For wss it goes through the
// HTTPS proxy of the environment ($HTTPS_PROXY, $NO_PROXY), if any.
func Dial(ctx context.Context, rawURL string, header http.Header) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	secure := false
	switch u.Scheme {
	case "wss":
		secure = true
	case "ws":
	default:
		return nil, fmt.Errorf("websocket: unsupported scheme %q", u.Scheme)
	}
	addr := u.Host
	if u.Port() == "" {
		if secure {
			addr = net.JoinHostPort(u.Hostname(), "443")
		} else {
			addr = net.JoinHostPort(u.Hostname(), "80")
		}
	}
	nc, err := dialTCP(ctx, addr, secure)
	if err != nil {
		return nil, err
	}
	if secure {
		tc := tls.Client(nc, &tls.Config{ServerName: u.Hostname()})
		if err := tc.HandshakeContext(ctx); err != nil {
			nc.Close()
			return nil, err
		}
		nc = tc
	}
	c, err := handshake(ctx, nc, u, header)
	if err != nil {
		nc.Close()
		return nil, err
	}
	return c, nil
}

// Proxy returns the proxy for a wss:// connection, like http.Transport's
// Proxy field. Tests replace it.
var Proxy = http.ProxyFromEnvironment

func dialTCP(ctx context.Context, addr string, secure bool) (net.Conn, error) {
	var d net.Dialer
	if !secure {
		return d.DialContext(ctx, "tcp", addr)
	}
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: addr}}
	proxy, err := Proxy(req)
	if err != nil || proxy == nil {
		return d.DialContext(ctx, "tcp", addr)
	}
	pa := proxy.Host
	if proxy.Port() == "" {
		pa = net.JoinHostPort(proxy.Hostname(), map[bool]string{true: "443", false: "80"}[proxy.Scheme == "https"])
	}
	nc, err := d.DialContext(ctx, "tcp", pa)
	if err != nil {
		return nil, err
	}
	if proxy.Scheme == "https" {
		tc := tls.Client(nc, &tls.Config{ServerName: proxy.Hostname()})
		if err := tc.HandshakeContext(ctx); err != nil {
			nc.Close()
			return nil, err
		}
		nc = tc
	}
	connect := "CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\n"
	if proxy.User != nil {
		pw, _ := proxy.User.Password()
		connect += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(proxy.User.Username()+":"+pw)) + "\r\n"
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = nc.SetDeadline(dl)
		defer nc.SetDeadline(time.Time{})
	}
	if _, err := io.WriteString(nc, connect+"\r\n"); err != nil {
		nc.Close()
		return nil, err
	}
	br := bufio.NewReader(nc)
	res, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		nc.Close()
		return nil, err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		nc.Close()
		return nil, fmt.Errorf("websocket: proxy CONNECT: %s", res.Status)
	}
	if br.Buffered() > 0 {
		nc.Close()
		return nil, errors.New("websocket: proxy sent data before the tunnel was up")
	}
	return nc, nil
}

func handshake(ctx context.Context, nc net.Conn, u *url.URL, header http.Header) (*Conn, error) {
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := u.RequestURI()
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n", path, u.Host)
	fmt.Fprintf(&b, "Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n", key)
	for k, vs := range header {
		for _, v := range vs {
			if strings.ContainsAny(k+v, "\r\n") {
				return nil, errors.New("websocket: invalid header")
			}
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")
	if dl, ok := ctx.Deadline(); ok {
		_ = nc.SetDeadline(dl)
		defer nc.SetDeadline(time.Time{})
	}
	if _, err := io.WriteString(nc, b.String()); err != nil {
		return nil, err
	}
	br := bufio.NewReader(nc)
	res, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		res.Body.Close()
		return nil, fmt.Errorf("websocket: handshake: %s %s", res.Status, strings.TrimSpace(string(body)))
	}
	if res.Header.Get("Sec-WebSocket-Accept") != acceptKey(key) {
		return nil, errors.New("websocket: handshake: bad Sec-WebSocket-Accept")
	}
	return &Conn{nc: nc, br: br, client: true}, nil
}

func acceptKey(key string) string {
	h := sha1.Sum([]byte(key + guid))
	return base64.StdEncoding.EncodeToString(h[:])
}

// Accept upgrades an HTTP request to a WebSocket connection (server
// side, used by test fakes).
func Accept(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || r.Header.Get("Sec-WebSocket-Version") != "13" {
		http.Error(w, "not a websocket handshake", http.StatusBadRequest)
		return nil, errors.New("websocket: not a websocket handshake")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("websocket: cannot hijack")
	}
	nc, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + acceptKey(key) + "\r\n\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		nc.Close()
		return nil, err
	}
	if err := brw.Flush(); err != nil {
		nc.Close()
		return nil, err
	}
	return &Conn{nc: nc, br: brw.Reader}, nil
}

// SetReadDeadline sets the deadline for ReadMessage.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.nc.SetReadDeadline(t) }

// ReadMessage returns the next text or binary message. It answers pings
// itself and returns *CloseError when the peer closes.
func (c *Conn) ReadMessage() (op int, data []byte, err error) {
	var msg []byte
	msgOp := -1
	for {
		fin, op, payload, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case OpPing:
			if err := c.writeFrame(OpPong, payload); err != nil {
				return 0, nil, err
			}
			continue
		case OpPong:
			continue
		case OpClose:
			ce := &CloseError{Code: 1005}
			if len(payload) >= 2 {
				ce.Code = int(binary.BigEndian.Uint16(payload))
				ce.Reason = string(payload[2:])
			}
			_ = c.writeFrame(OpClose, payload[:min(len(payload), 2)])
			c.nc.Close()
			return 0, nil, ce
		case OpText, OpBinary:
			if msgOp != -1 {
				return 0, nil, errors.New("websocket: new message inside a fragmented one")
			}
			msgOp = op
		case OpContinuation:
			if msgOp == -1 {
				return 0, nil, errors.New("websocket: continuation without a message")
			}
		default:
			return 0, nil, fmt.Errorf("websocket: unknown opcode %d", op)
		}
		if len(msg)+len(payload) > MaxMessage {
			return 0, nil, errors.New("websocket: message too large")
		}
		msg = append(msg, payload...)
		if fin {
			return msgOp, msg, nil
		}
	}
}

func (c *Conn) readFrame() (fin bool, op int, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.br, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	op = int(h[0] & 0x0f)
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > MaxMessage {
		err = errors.New("websocket: frame too large")
		return
	}
	if op >= 0x8 && (n > 125 || !fin) {
		err = errors.New("websocket: invalid control frame")
		return
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return
}

// WriteText sends a text message.
func (c *Conn) WriteText(data []byte) error { return c.writeFrame(OpText, data) }

// WriteJSON sends v as a JSON text message.
func (c *Conn) WriteJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteText(data)
}

// Ping sends a ping; the peer's pong is consumed by ReadMessage.
func (c *Conn) Ping() error { return c.writeFrame(OpPing, nil) }

func (c *Conn) writeFrame(op int, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	buf := make([]byte, 0, len(payload)+14)
	buf = append(buf, 0x80|byte(op))
	maskBit := byte(0)
	if c.client {
		maskBit = 0x80
	}
	switch n := len(payload); {
	case n < 126:
		buf = append(buf, maskBit|byte(n))
	case n <= 0xffff:
		buf = append(buf, maskBit|126, byte(n>>8), byte(n))
	default:
		buf = append(buf, maskBit|127)
		buf = binary.BigEndian.AppendUint64(buf, uint64(n))
	}
	if c.client {
		var mask [4]byte
		if _, err := rand.Read(mask[:]); err != nil {
			return err
		}
		buf = append(buf, mask[:]...)
		start := len(buf)
		buf = append(buf, payload...)
		for i := range payload {
			buf[start+i] ^= mask[i%4]
		}
	} else {
		buf = append(buf, payload...)
	}
	_, err := c.nc.Write(buf)
	if op == OpClose {
		c.closed = true
	}
	return err
}

// Close sends a normal close frame and closes the connection.
func (c *Conn) Close() error {
	_ = c.writeFrame(OpClose, []byte{0x03, 0xe8}) // 1000
	return c.nc.Close()
}
