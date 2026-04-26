package qmux

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/okdaichi/qmux-go/qmux/internal/wire"
)

func TestConn_ConnectionStats(t *testing.T) {
	server, client := setupPair(t, nil, nil)
	
	// Send some data to populate traffic stats
	str, err := client.OpenStream()
	require.NoError(t, err)
	_, err = str.Write([]byte("hello"))
	require.NoError(t, err)
	
	// Wait a bit for data to be processed
	time.Sleep(100 * time.Millisecond)
	
	stats := client.ConnectionStats()
	assert.Greater(t, stats.BytesSent, uint64(0))
	assert.Greater(t, stats.PacketsSent, uint64(0))
	
	statsServer := server.ConnectionStats()
	assert.Greater(t, statsServer.BytesReceived, uint64(0))
	assert.Greater(t, statsServer.PacketsReceived, uint64(0))

	// Test RTT calculation via manual ping response to control timing
	now := time.Now()
	// Sequence is used to store start time in nanoseconds
	ping := &wire.PingFrame{
		TypeField: wire.FrameTypePingResponse,
		Sequence:  uint64(now.Add(-100 * time.Millisecond).UnixNano()),
	}
	
	client.handlePingFrame(ping)
	
	stats = client.ConnectionStats()
	// RTT should be around 100ms
	assert.GreaterOrEqual(t, stats.LatestRTT, 100*time.Millisecond)
	assert.Less(t, stats.LatestRTT, 200*time.Millisecond)
	assert.Equal(t, stats.LatestRTT, stats.MinRTT)
	assert.Equal(t, stats.LatestRTT, stats.SmoothedRTT)
}

func TestConn_ApplicationProtocolNegotiation(t *testing.T) {
	config := DefaultConfig()
	config.ApplicationProtocols = []string{"moq-v1"}
	
	server, client := setupPair(t, config, config)
	
	// Wait for handshake
	time.Sleep(100 * time.Millisecond)
	
	assert.Equal(t, "moq-v1", client.ConnectionState().TLS.NegotiatedProtocol)
	assert.Equal(t, "moq-v1", server.ConnectionState().TLS.NegotiatedProtocol)
}
