package qmux

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func BenchmarkStream_Throughput(b *testing.B) {
	client, server := newTestPair(b, nil, nil)
	ctx := testContext(b)

	done := make(chan struct{})
	go func() {
		defer close(done)
		s, err := server.AcceptUniStream(ctx)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, s) // not actionable: the benchmark fails on the write side
	}()

	s, err := client.OpenUniStreamSync(ctx)
	require.NoError(b, err)
	data := make([]byte, 32*1024)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		_, err := s.Write(data)
		require.NoError(b, err)
	}
	require.NoError(b, s.Close())
	<-done
}

// One small stream per message, the shape of one MoQ group per frame.
func BenchmarkConn_StreamPerMessage(b *testing.B) {
	client, server := newTestPair(b, nil, nil)
	ctx := testContext(b)

	go func() {
		for {
			s, err := server.AcceptUniStream(ctx)
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, s) // not actionable: the benchmark fails on the write side
		}
	}()

	data := make([]byte, 160)
	b.ReportAllocs()
	for b.Loop() {
		s, err := client.OpenUniStreamSync(ctx)
		require.NoError(b, err)
		_, err = s.Write(data)
		require.NoError(b, err)
		require.NoError(b, s.Close())
	}
}
