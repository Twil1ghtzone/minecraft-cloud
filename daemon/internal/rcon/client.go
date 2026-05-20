// Package rcon implements the Valve Source RCON protocol used by Minecraft Paper/Spigot.
package rcon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	packetTypeAuth       = 3
	packetTypeAuthResp   = 2
	packetTypeCommand    = 2
	packetTypeCommandResp = 0

	maxPacketSize = 4096
	readTimeout   = 10 * time.Second
)

// Client is a thread-safe RCON connection to a single Minecraft server.
type Client struct {
	conn    net.Conn
	mu      sync.Mutex
	counter int32
}

// Dial connects to the RCON server at addr and authenticates with password.
func Dial(addr, password string) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("rcon dial: %w", err)
	}
	c := &Client{conn: conn}
	if err := c.auth(password); err != nil {
		conn.Close()
		return nil, fmt.Errorf("rcon auth: %w", err)
	}
	return c, nil
}

// Command sends a command and returns the server's response.
func (c *Client) Command(cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := int32(atomic.AddInt32(&c.counter, 1))
	if err := c.writePacket(id, packetTypeCommand, cmd); err != nil {
		return "", err
	}
	// Read one or more response packets until we see our request ID.
	var out bytes.Buffer
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(readTimeout))
		rid, _, body, err := c.readPacket()
		if err != nil {
			return out.String(), err
		}
		out.Write(body)
		if rid == id {
			break
		}
	}
	return out.String(), nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) auth(password string) error {
	if err := c.writePacket(1, packetTypeAuth, password); err != nil {
		return err
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(readTimeout))
	_, ptype, _, err := c.readPacket()
	if err != nil {
		return err
	}
	if ptype == -1 {
		return errors.New("authentication failed")
	}
	return nil
}

func (c *Client) writePacket(id, ptype int32, body string) error {
	// Packet: size(4) + id(4) + type(4) + body + null + null
	bodyBytes := []byte(body)
	size := int32(4 + 4 + len(bodyBytes) + 2)
	buf := make([]byte, 4+size)
	binary.LittleEndian.PutUint32(buf[0:], uint32(size))
	binary.LittleEndian.PutUint32(buf[4:], uint32(id))
	binary.LittleEndian.PutUint32(buf[8:], uint32(ptype))
	copy(buf[12:], bodyBytes)
	// Two null terminators already zero from make()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := c.conn.Write(buf)
	return err
}

func (c *Client) readPacket() (id, ptype int32, body []byte, err error) {
	var sizeBuf [4]byte
	if _, err = io.ReadFull(c.conn, sizeBuf[:]); err != nil {
		return
	}
	size := int(binary.LittleEndian.Uint32(sizeBuf[:]))
	if size < 10 || size > maxPacketSize {
		return 0, 0, nil, fmt.Errorf("rcon: invalid packet size %d", size)
	}
	pkt := make([]byte, size)
	if _, err = io.ReadFull(c.conn, pkt); err != nil {
		return
	}
	id = int32(binary.LittleEndian.Uint32(pkt[0:]))
	ptype = int32(binary.LittleEndian.Uint32(pkt[4:]))
	// body is everything between byte 8 and the two trailing nulls
	if size > 10 {
		body = pkt[8 : size-2]
	}
	return
}

// DialWithRetry tries to connect up to maxRetries times with a backoff.
// Useful for waiting for a server to start its RCON listener.
func DialWithRetry(addr, password string, maxRetries int, backoff time.Duration) (*Client, error) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		c, err := Dial(addr, password)
		if err == nil {
			return c, nil
		}
		lastErr = err
		time.Sleep(backoff)
	}
	return nil, fmt.Errorf("rcon: max retries exceeded: %w", lastErr)
}
