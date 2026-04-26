package qmux

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStream_Cancel(t *testing.T) {
	tests := map[string]struct {
		run func(t *testing.T, server, client *Conn)
	}{
		"CancelWrite": {
			run: func(t *testing.T, server, client *Conn) {
				strClient, err := client.OpenStream()
				require.NoError(t, err)

				strServer, err := server.AcceptStream(context.Background())
				require.NoError(t, err)

				strClient.CancelWrite(0x42)

				buf := make([]byte, 1024)
				_, err = strServer.Read(buf)
				require.Error(t, err)
				var streamErr *quic.StreamError
				require.ErrorAs(t, err, &streamErr)
				assert.Equal(t, StreamErrorCode(0x42), streamErr.ErrorCode)
			},
		},
		"CancelRead": {
			run: func(t *testing.T, server, client *Conn) {
				strClient, err := client.OpenStream()
				require.NoError(t, err)

				strServer, err := server.AcceptStream(context.Background())
				require.NoError(t, err)

				strServer.CancelRead(0x43)

				// Wait a bit for the STOP_SENDING to arrive and trigger RESET_STREAM
				time.Sleep(200 * time.Millisecond)

				_, err = strClient.Write([]byte("denied"))
				require.Error(t, err)
				var streamErr *quic.StreamError
				require.ErrorAs(t, err, &streamErr)
				assert.Equal(t, StreamErrorCode(0x43), streamErr.ErrorCode)
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server, client := setupPair(t, nil, nil)
			tt.run(t, server, client)
		})
	}
}

func TestStream_Deadlines(t *testing.T) {
	tests := map[string]struct {
		run func(t *testing.T, server, client *Conn)
	}{
		"ReadDeadline": {
			run: func(t *testing.T, server, client *Conn) {
				strClient, err := client.OpenStream()
				require.NoError(t, err)

				require.NoError(t, strClient.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
				buf := make([]byte, 1024)
				_, err = strClient.Read(buf)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "deadline exceeded")
			},
		},
		"WriteDeadline": {
			run: func(t *testing.T, server, client *Conn) {
				strClient, err := client.OpenStream()
				require.NoError(t, err)

				require.NoError(t, strClient.SetWriteDeadline(time.Now().Add(-1*time.Second)))
				_, err = strClient.Write([]byte("lost in time"))
				require.Error(t, err)
				assert.Contains(t, err.Error(), "deadline exceeded")
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server, client := setupPair(t, nil, nil)
			tt.run(t, server, client)
		})
	}
}

func TestFlowControl(t *testing.T) {
	config := &Config{
		MaxIncomingStreams:             10,
		InitialStreamReceiveWindow:     1024,
		InitialConnectionReceiveWindow: 2048,
		MaxIdleTimeout:                 30 * time.Second,
	}
	server, client := setupPair(t, config, config)

	strClient, err := client.OpenStream()
	require.NoError(t, err)

	largeData := make([]byte, 3000)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	errChan := make(chan error, 1)
	go func() {
		str, err := server.AcceptStream(context.Background())
		if err != nil {
			errChan <- err
			return
		}
		buf := make([]byte, 3000)
		_, err = io.ReadFull(str, buf)
		if err != nil {
			errChan <- err
			return
		}
		if string(buf) != string(largeData) {
			errChan <- assert.AnError
			return
		}
		errChan <- nil
	}()

	_, err = strClient.Write(largeData)
	assert.NoError(t, err)
	require.NoError(t, strClient.Close())

	assert.NoError(t, <-errChan)
}
