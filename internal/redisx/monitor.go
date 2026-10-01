package redisx

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// MonitorConn is a raw connection in MONITOR mode. go-redis' MonitorCmd polls
// the connection in a busy loop and shares it with the pool, so MONITOR uses
// its own minimal RESP2 connection.
type MonitorConn struct {
	conn net.Conn
	rd   *bufio.Reader
}

// Monitor opens a dedicated connection to the node and switches it to MONITOR.
func (c *Client) Monitor(ctx context.Context, node Node) (*MonitorConn, error) {
	opt := c.nodeOptions(node.Addr, 0)
	if node.Role == RoleSentinel {
		opt = c.sentinelOptions(node.Addr)
	}

	conn, err := (&net.Dialer{Timeout: opt.DialTimeout}).DialContext(ctx, "tcp", node.Addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", node.Addr, err)
	}

	_ = conn.SetDeadline(time.Now().Add(opt.DialTimeout))

	if c.tlsConfig != nil {
		cfg := c.tlsConfig.Clone()
		if cfg.ServerName == "" {
			cfg.ServerName, _, _ = net.SplitHostPort(node.Addr)
		}

		tlsConn := tls.Client(conn, cfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()

			return nil, fmt.Errorf("tls handshake with %s: %w", node.Addr, err)
		}

		conn = tlsConn
	}

	m := &MonitorConn{conn: conn, rd: bufio.NewReader(conn)}

	var steps [][]string

	switch {
	case opt.Password != "" && opt.Username != "":
		steps = append(steps, []string{"AUTH", opt.Username, opt.Password})
	case opt.Password != "":
		steps = append(steps, []string{"AUTH", opt.Password})
	}

	steps = append(steps, []string{"MONITOR"})

	for _, args := range steps {
		if err := m.command(args...); err != nil {
			_ = conn.Close()

			return nil, fmt.Errorf("%s on %s: %w", args[0], node.Addr, err)
		}
	}

	_ = conn.SetDeadline(time.Time{})

	return m, nil
}

func (m *MonitorConn) command(args ...string) error {
	var b strings.Builder

	b.WriteString("*" + strconv.Itoa(len(args)) + "\r\n")

	for _, arg := range args {
		b.WriteString("$" + strconv.Itoa(len(arg)) + "\r\n" + arg + "\r\n")
	}

	if _, err := m.conn.Write([]byte(b.String())); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	_, err := m.Next()

	return err
}

// Next returns the next line: the reply to a command or a monitored command.
func (m *MonitorConn) Next() (string, error) {
	line, err := m.rd.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	line = strings.TrimRight(line, "\r\n")

	switch {
	case strings.HasPrefix(line, "+"):
		return line[1:], nil
	case strings.HasPrefix(line, "-"):
		return "", errors.New(line[1:])
	default:
		return "", fmt.Errorf("unexpected reply %q", line)
	}
}

func (m *MonitorConn) Close() error {
	return m.conn.Close() //nolint:wrapcheck // closing a raw connection
}
