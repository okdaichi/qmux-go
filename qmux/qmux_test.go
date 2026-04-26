package qmux

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupPair creates a pair of QMux connections connected via a local TCP connection.
func setupPair(tb testing.TB, serverConfig, clientConfig *Config) (*Conn, *Conn) {
	tb.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = ln.Close() })

	type connResult struct {
		conn *Conn
		err  error
	}
	serverChan := make(chan connResult, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverChan <- connResult{nil, err}
			return
		}
		s, err := Server(conn, serverConfig)
		if err != nil {
			_ = conn.Close()
		}
		serverChan <- connResult{s, err}
	}()

	clientRaw, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(tb, err)
	clientConn, err := Dial(clientRaw, clientConfig)
	if err != nil {
		_ = clientRaw.Close()
		require.NoError(tb, err, "Dial failed")
	}

	var serverConn *Conn
	select {
	case res := <-serverChan:
		require.NoError(tb, res.err, "Server startup failed")
		serverConn = res.conn
	case <-time.After(5 * time.Second):
		tb.Fatal("Server connection timed out")
	}

	tb.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	return serverConn, clientConn
}

func TestBasic(t *testing.T) {
	server, client := setupPair(t, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientDone := make(chan struct{})
	errChan := make(chan error, 1)

	go func() {
		str, err := server.AcceptStream(ctx)
		if err != nil {
			errChan <- fmt.Errorf("AcceptStream failed: %w", err)
			return
		}
		if str == nil {
			errChan <- fmt.Errorf("AcceptStream returned nil str")
			return
		}
		defer str.Close()

		buf := make([]byte, 1024)
		n, err := str.Read(buf)
		if err != nil {
			errChan <- fmt.Errorf("Read failed: %w", err)
			return
		}

		if string(buf[:n]) != "hello from client" {
			errChan <- fmt.Errorf("unexpected data: %s", string(buf[:n]))
			return
		}

		_, err = str.Write([]byte("hello from server"))
		if err != nil {
			errChan <- fmt.Errorf("Write failed: %w", err)
			return
		}

		select {
		case <-clientDone:
		case <-ctx.Done():
		}
		errChan <- nil
	}()

	str, err := client.OpenStream()
	require.NoError(t, err)
	defer str.Close()

	_, err = str.Write([]byte("hello from client"))
	assert.NoError(t, err)

	buf := make([]byte, 1024)
	n, err := str.Read(buf)
	assert.NoError(t, err)
	assert.Equal(t, "hello from server", string(buf[:n]))

	close(clientDone)
	assert.NoError(t, <-errChan)
}
