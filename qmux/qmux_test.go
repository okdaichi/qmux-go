package qmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

// setupQMuxPair creates a pair of QMux connections connected via a net.Pipe.
func setupQMuxPair(t *testing.T, serverConfig, clientConfig *Config) (*Conn, *Conn) {
	c1, c2 := net.Pipe()
	
	errChan := make(chan error, 1)
	var serverConn *Conn
	go func() {
		var err error
		serverConn, err = Server(c1, serverConfig)
		errChan <- err
	}()

	clientConn, err := Dial(c2, clientConfig)
	if err != nil {
		c1.Close()
		c2.Close()
		t.Fatalf("Dial failed: %v", err)
	}

	if err := <-errChan; err != nil {
		c1.Close()
		c2.Close()
		t.Fatalf("Server startup failed: %v", err)
	}

	t.Cleanup(func() {
		serverConn.Close()
		clientConn.Close()
	})

	return serverConn, clientConn
}

func TestQMuxBasic(t *testing.T) {
	server, client := setupQMuxPair(t, nil, nil)
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
			errChan <- errors.New("AcceptStream returned nil str")
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
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer str.Close()

	_, err = str.Write([]byte("hello from client"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	buf := make([]byte, 1024)
	n, err := str.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if string(buf[:n]) != "hello from server" {
		t.Errorf("unexpected data from server: %s", string(buf[:n]))
	}

	close(clientDone)
	if err := <-errChan; err != nil {
		t.Errorf("server error: %v", err)
	}
}

func TestQMuxUniStream(t *testing.T) {
	server, client := setupQMuxPair(t, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	data := "unidirectional payload"
	errChan := make(chan error, 1)

	go func() {
		str, err := server.AcceptUniStream(ctx)
		if err != nil {
			errChan <- fmt.Errorf("AcceptUniStream failed: %w", err)
			return
		}
		if str == nil {
			errChan <- errors.New("AcceptUniStream returned nil str")
			return
		}

		buf, err := io.ReadAll(str)
		if err != nil {
			errChan <- fmt.Errorf("ReadAll failed: %w", err)
			return
		}

		if string(buf) != data {
			errChan <- fmt.Errorf("expected %q, got %q", data, string(buf))
			return
		}
		errChan <- nil
	}()

	str, err := client.OpenUniStream()
	if err != nil {
		t.Fatalf("OpenUniStream failed: %v", err)
	}

	_, err = str.Write([]byte(data))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	str.Close()

	if err := <-errChan; err != nil {
		t.Errorf("server error: %v", err)
	}
}

func TestQMuxConcurrentStreams(t *testing.T) {
	server, client := setupQMuxPair(t, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	numStreams := 10
	var wg sync.WaitGroup
	wg.Add(numStreams)

	for i := 0; i < numStreams; i++ {
		go func(idx int) {
			defer wg.Done()
			str, err := server.AcceptStream(ctx)
			if err != nil {
				t.Errorf("AcceptStream %d failed: %v", idx, err)
				return
			}
			if str == nil {
				t.Errorf("AcceptStream %d returned nil str", idx)
				return
			}
			defer str.Close()

			buf := make([]byte, 1024)
			n, err := str.Read(buf)
			if err != nil {
				t.Errorf("Read %d failed: %v", idx, err)
				return
			}
			// Echo back
			_, _ = str.Write(buf[:n])
		}(i)
	}

	for i := 0; i < numStreams; i++ {
		str, err := client.OpenStream()
		if err != nil {
			t.Fatalf("OpenStream %d failed: %v", i, err)
		}
		msg := fmt.Sprintf("msg-%d", i)
		_, _ = str.Write([]byte(msg))

		buf := make([]byte, 1024)
		n, err := str.Read(buf)
		if err != nil {
			t.Errorf("stream %d Read failed: %v", i, err)
		} else if string(buf[:n]) != msg {
			t.Errorf("stream %d: expected %q, got %q", i, msg, string(buf[:n]))
		}
		str.Close()
	}

	wg.Wait()
}

func TestQMuxStreamCancel(t *testing.T) {
	t.Run("CancelWrite", func(t *testing.T) {
		server, client := setupQMuxPair(t, nil, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		strClient, err := client.OpenStream()
		if err != nil {
			t.Fatalf("OpenStream failed: %v", err)
		}
		strServer, err := server.AcceptStream(ctx)
		if err != nil {
			t.Fatalf("AcceptStream failed: %v", err)
		}

		strClient.CancelWrite(quic.StreamErrorCode(0x42))
		
		buf := make([]byte, 10)
		_, err = strServer.Read(buf)
		if err == nil {
			t.Fatal("expected error on server Read after client CancelWrite")
		}
		var streamErr *quic.StreamError
		if !errors.As(err, &streamErr) || streamErr.ErrorCode != 0x42 {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("CancelRead", func(t *testing.T) {
		server, client := setupQMuxPair(t, nil, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		strClient, err := client.OpenStream()
		if err != nil {
			t.Fatalf("OpenStream failed: %v", err)
		}
		strServer, err := server.AcceptStream(ctx)
		if err != nil {
			t.Fatalf("AcceptStream failed: %v", err)
		}

		strServer.CancelRead(quic.StreamErrorCode(0x43))
		
		// Wait a bit for the STOP_SENDING to propagate and trigger RESET_STREAM response
		time.Sleep(200 * time.Millisecond)

		_, err = strClient.Write([]byte("too late"))
		if err == nil {
			t.Fatal("expected error on client Write after server CancelRead")
		}
	})
}

func TestQMuxDeadlines(t *testing.T) {
	server, client := setupQMuxPair(t, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	strClient, err := client.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	_, err = server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream failed: %v", err)
	}

	t.Run("ReadDeadline", func(t *testing.T) {
		strClient.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		buf := make([]byte, 10)
		_, err := strClient.Read(buf)
		if err == nil || err.Error() != "deadline exceeded" {
			t.Errorf("expected deadline exceeded, got: %v", err)
		}
	})

	t.Run("WriteDeadline", func(t *testing.T) {
		strClient.SetWriteDeadline(time.Now().Add(-1 * time.Second))
		_, err := strClient.Write([]byte("lost in time"))
		if err == nil || err.Error() != "deadline exceeded" {
			t.Errorf("expected deadline exceeded, got: %v", err)
		}
	})
}

func TestQMuxFlowControl(t *testing.T) {
	config := &Config{
		InitialStreamReceiveWindow:     1024,
		InitialConnectionReceiveWindow: 2048,
	}
	server, client := setupQMuxPair(t, config, config)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	largeData := make([]byte, 10240)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	errChan := make(chan error, 1)
	go func() {
		str, err := server.AcceptStream(ctx)
		if err != nil {
			errChan <- fmt.Errorf("AcceptStream failed: %w", err)
			return
		}
		received, err := io.ReadAll(str)
		if err != nil {
			errChan <- fmt.Errorf("ReadAll failed: %w", err)
			return
		}
		if len(received) != len(largeData) {
			errChan <- fmt.Errorf("got %d bytes, want %d", len(received), len(largeData))
			return
		}
		errChan <- nil
	}()

	str, err := client.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	_, err = str.Write(largeData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	str.Close()

	if err := <-errChan; err != nil {
		t.Errorf("server error: %v", err)
	}
}

func TestQMuxCloseWithError(t *testing.T) {
	server, client := setupQMuxPair(t, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	strClient, err := client.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	strServer, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream failed: %v", err)
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		client.CloseWithError(0x1234, "goodbye")
	}()

	buf := make([]byte, 10)
	_, err = strServer.Read(buf)
	if err == nil {
		t.Fatal("expected error on Read after CloseWithError")
	}
	
	var appErr *quic.ApplicationError
	if !errors.As(err, &appErr) || appErr.ErrorCode != 0x1234 || appErr.ErrorMessage != "goodbye" {
		t.Errorf("unexpected error: %v", err)
	}

	_, err = strClient.Write([]byte("denied"))
	if err == nil {
		t.Fatal("expected error on Write after CloseWithError")
	}
}
