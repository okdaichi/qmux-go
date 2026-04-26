package qmux

import (
	"context"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUniStream(t *testing.T) {
	server, client := setupPair(t, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		str, err := server.AcceptUniStream(ctx)
		if err != nil {
			errChan <- err
			return
		}
		buf := make([]byte, 1024)
		n, err := str.Read(buf)
		if err != nil {
			errChan <- err
			return
		}
		if string(buf[:n]) != "uni data" {
			errChan <- assert.AnError
			return
		}
		errChan <- nil
	}()

	str, err := client.OpenUniStream()
	require.NoError(t, err)
	_, err = str.Write([]byte("uni data"))
	assert.NoError(t, err)
	require.NoError(t, str.Close())

	assert.NoError(t, <-errChan)
}

func TestConcurrentStreams(t *testing.T) {
	server, client := setupPair(t, nil, nil)

	const numStreams = 10
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		for range numStreams {
			str, err := server.AcceptStream(ctx)
			if err != nil {
				return
			}
			go func(s *Stream) {
				buf := make([]byte, 10)
				n, _ := s.Read(buf)
				_, _ = s.Write(buf[:n])
				_ = s.Close()
			}(str)
		}
	}()

	for range numStreams {
		str, err := client.OpenStream()
		require.NoError(t, err)
		_, err = str.Write([]byte("ping"))
		assert.NoError(t, err)
		buf := make([]byte, 10)
		n, err := str.Read(buf)
		assert.NoError(t, err)
		assert.Equal(t, "ping", string(buf[:n]))
		_ = str.Close()
	}
}

func TestCloseWithError(t *testing.T) {
	server, client := setupPair(t, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		str, err := server.AcceptStream(ctx)
		if err != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
		_ = server.CloseWithError(0x1234, "goodbye")
		_ = str.Close()
	}()

	strClient, err := client.OpenStream()
	require.NoError(t, err)

	buf := make([]byte, 1024)
	_, err = strClient.Read(buf)
	require.Error(t, err)

	var appErr *quic.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, quic.ApplicationErrorCode(0x1234), appErr.ErrorCode)
	assert.Equal(t, "goodbye", appErr.ErrorMessage)

	_, err = strClient.Write([]byte("denied"))
	assert.Error(t, err)
}
