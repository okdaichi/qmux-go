package qmux

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/okdaichi/qmux-go/qmux/internal/wire"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/quicvarint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testTimeout = 10 * time.Second

// newTestPair returns the two ends of a QMux connection over TCP on the
// loopback interface.
func newTestPair(tb testing.TB, clientConfig, serverConfig *Config) (client, server *Conn) {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(tb, err)
	defer func() { assert.NoError(tb, ln.Close()) }()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- conn
	}()
	clientConn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(tb, err)
	serverConn, ok := <-accepted
	require.True(tb, ok, "accept failed")

	client, err = Dial(clientConn, clientConfig)
	require.NoError(tb, err)
	server, err = Server(serverConn, serverConfig)
	require.NoError(tb, err)
	tb.Cleanup(func() {
		assert.NoError(tb, client.Close())
		assert.NoError(tb, server.Close())
		// Every goroutine of a closed connection ends.
		client.loops.Wait()
		server.loops.Wait()
	})
	return client, server
}

func testContext(tb testing.TB) context.Context {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	tb.Cleanup(cancel)
	return ctx
}

func streamCount(c *Conn) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.streams)
}

// One short stream after another, as with one MoQ group per stream, has to
// keep working past the initial stream limit, and leave nothing behind.
func TestConn_OpenUniStreamSync_PastInitialLimit(t *testing.T) {
	const streams = 500
	client, server := newTestPair(t, nil, &Config{MaxIncomingUniStreams: 4})
	ctx := testContext(t)

	received := make(chan []byte, streams)
	go func() {
		for {
			s, err := server.AcceptUniStream(ctx)
			if err != nil {
				return
			}
			p, err := io.ReadAll(s)
			if err != nil {
				return
			}
			received <- p
		}
	}()

	for i := range streams {
		s, err := client.OpenUniStreamSync(ctx)
		require.NoError(t, err, "stream %d", i)
		_, err = s.Write([]byte{byte(i), byte(i >> 8)})
		require.NoError(t, err)
		require.NoError(t, s.Close())
	}
	for i := range streams {
		select {
		case p := <-received:
			assert.Equal(t, []byte{byte(i), byte(i >> 8)}, p)
		case <-ctx.Done():
			require.FailNow(t, "timed out", "received %d of %d streams", i, streams)
		}
	}
	assert.Zero(t, streamCount(client), "client keeps finished streams")
	assert.Eventually(t, func() bool { return streamCount(server) == 0 }, testTimeout, time.Millisecond,
		"server keeps finished streams")
}

func TestConn_OpenStream_LimitReached(t *testing.T) {
	client, server := newTestPair(t, nil, &Config{MaxIncomingStreams: 1})
	ctx := testContext(t)

	first, err := client.OpenStreamSync(ctx)
	require.NoError(t, err)
	_, err = client.OpenStream()
	require.ErrorIs(t, err, quic.StreamLimitReachedError{})

	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = client.OpenStreamSync(short)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// The limit moves once both directions of the first stream have ended.
	_, err = first.Write([]byte("ping"))
	require.NoError(t, err)
	require.NoError(t, first.Close())
	accepted, err := server.AcceptStream(ctx)
	require.NoError(t, err)
	_, err = io.ReadAll(accepted)
	require.NoError(t, err)
	require.NoError(t, accepted.Close())

	second, err := client.OpenStreamSync(ctx)
	require.NoError(t, err)
	assert.Equal(t, StreamID(4), second.StreamID())
}

// io.Writer forbids retaining p: a caller that reuses its buffer must not
// corrupt what it wrote before.
func TestSendStream_Write_DoesNotRetainBuffer(t *testing.T) {
	const writes = 300
	client, server := newTestPair(t, nil, nil)
	ctx := testContext(t)

	s, err := client.OpenUniStreamSync(ctx)
	require.NoError(t, err)
	var expected []byte
	buf := make([]byte, 64)
	for i := range writes {
		for j := range buf {
			buf[j] = byte(i)
		}
		expected = append(expected, buf...)
		_, err := s.Write(buf)
		require.NoError(t, err)
	}
	require.NoError(t, s.Close())

	r, err := server.AcceptUniStream(ctx)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(expected, got), "data differs from what was written")
}

// A transfer many times the size of the flow control windows, echoed back.
func TestStream_Write_LargeTransfer(t *testing.T) {
	config := &Config{InitialStreamReceiveWindow: 32 * 1024, InitialConnectionReceiveWindow: 48 * 1024}
	client, server := newTestPair(t, config, config)
	ctx := testContext(t)

	go func() {
		s, err := server.AcceptStream(ctx)
		if err != nil {
			return
		}
		if _, err := io.Copy(s, s); err != nil {
			return
		}
		assert.NoError(t, s.Close())
	}()

	data := make([]byte, 4<<20)
	_, err := rand.Read(data)
	require.NoError(t, err)

	s, err := client.OpenStreamSync(ctx)
	require.NoError(t, err)
	go func() {
		if _, err := s.Write(data); err != nil {
			return
		}
		assert.NoError(t, s.Close())
	}()
	got, err := io.ReadAll(s)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(data, got), "echoed data differs")
}

func TestSendStream_CancelWrite(t *testing.T) {
	client, server := newTestPair(t, nil, nil)
	ctx := testContext(t)

	s, err := client.OpenUniStreamSync(ctx)
	require.NoError(t, err)
	_, err = s.Write([]byte("partial"))
	require.NoError(t, err)
	s.CancelWrite(7)

	var local *quic.StreamError
	_, err = s.Write([]byte("more"))
	require.ErrorAs(t, err, &local)
	assert.Equal(t, quic.StreamError{StreamID: s.StreamID(), ErrorCode: 7}, *local)
	assert.ErrorIs(t, context.Cause(s.Context()), local)
	assert.NoError(t, s.Close(), "Close after CancelWrite is a no-op")

	r, err := server.AcceptUniStream(ctx)
	require.NoError(t, err)
	var remote *quic.StreamError
	_, err = io.ReadAll(r)
	require.ErrorAs(t, err, &remote)
	assert.Equal(t, quic.StreamError{StreamID: s.StreamID(), ErrorCode: 7, Remote: true}, *remote)

	assert.Zero(t, streamCount(client))
	assert.Eventually(t, func() bool { return streamCount(server) == 0 }, testTimeout, time.Millisecond)
}

// A writer blocked on flow control is released when the peer stops reading.
func TestReceiveStream_CancelRead(t *testing.T) {
	config := &Config{InitialStreamReceiveWindow: 16 * 1024}
	client, server := newTestPair(t, config, config)
	ctx := testContext(t)

	s, err := client.OpenUniStreamSync(ctx)
	require.NoError(t, err)
	writeErr := make(chan error, 1)
	go func() {
		_, err := s.Write(make([]byte, 1<<20))
		writeErr <- err
	}()

	r, err := server.AcceptUniStream(ctx)
	require.NoError(t, err)
	r.CancelRead(9)

	var local *quic.StreamError
	_, err = r.Read(make([]byte, 1))
	require.ErrorAs(t, err, &local)
	assert.Equal(t, quic.StreamError{StreamID: r.StreamID(), ErrorCode: 9}, *local)

	select {
	case err := <-writeErr:
		var remote *quic.StreamError
		require.ErrorAs(t, err, &remote)
		assert.Equal(t, quic.StreamError{StreamID: s.StreamID(), ErrorCode: 9, Remote: true}, *remote)
	case <-ctx.Done():
		require.FailNow(t, "Write stayed blocked after the peer cancelled reading")
	}
	<-s.Context().Done()

	assert.Eventually(t, func() bool { return streamCount(client) == 0 && streamCount(server) == 0 },
		testTimeout, time.Millisecond)
}

func TestStream_SetDeadline(t *testing.T) {
	config := &Config{InitialStreamReceiveWindow: 16 * 1024}
	client, _ := newTestPair(t, config, config)
	ctx := testContext(t)

	s, err := client.OpenStreamSync(ctx)
	require.NoError(t, err)

	require.NoError(t, s.SetReadDeadline(time.Now().Add(20*time.Millisecond)))
	_, err = s.Read(make([]byte, 1))
	assert.ErrorIs(t, err, os.ErrDeadlineExceeded)

	// Nobody reads the stream, so the write stops at the peer's window.
	require.NoError(t, s.SetWriteDeadline(time.Now().Add(20*time.Millisecond)))
	n, err := s.Write(make([]byte, 1<<20))
	assert.ErrorIs(t, err, os.ErrDeadlineExceeded)
	assert.Equal(t, 16*1024, n)

	require.NoError(t, s.SetDeadline(time.Now().Add(-time.Second)))
	_, err = s.Read(make([]byte, 1))
	assert.ErrorIs(t, err, os.ErrDeadlineExceeded)

	// A deadline that has passed fails a write that flow control allows.
	fresh, err := client.OpenStreamSync(ctx)
	require.NoError(t, err)
	require.NoError(t, fresh.SetWriteDeadline(time.Now().Add(-time.Second)))
	n, err = fresh.Write([]byte("late"))
	assert.ErrorIs(t, err, os.ErrDeadlineExceeded)
	assert.Zero(t, n)
}

func TestSendStream_Context(t *testing.T) {
	client, _ := newTestPair(t, nil, nil)
	ctx := testContext(t)

	s, err := client.OpenUniStreamSync(ctx)
	require.NoError(t, err)
	require.NoError(t, s.Context().Err())
	require.NoError(t, s.Close())
	assert.ErrorIs(t, s.Context().Err(), context.Canceled)

	_, err = s.Write([]byte("late"))
	assert.Error(t, err)
}

func TestConn_CloseWithError(t *testing.T) {
	client, server := newTestPair(t, nil, nil)
	ctx := testContext(t)

	s, err := client.OpenStreamSync(ctx)
	require.NoError(t, err)
	_, err = s.Write([]byte("hello"))
	require.NoError(t, err)
	accepted, err := server.AcceptStream(ctx)
	require.NoError(t, err)

	require.NoError(t, client.CloseWithError(42, "done"))

	var local *quic.ApplicationError
	_, err = s.Write([]byte("late"))
	require.ErrorAs(t, err, &local)
	assert.Equal(t, quic.ApplicationError{ErrorCode: 42, ErrorMessage: "done"}, *local)
	_, err = client.OpenStreamSync(ctx)
	assert.ErrorAs(t, err, &local)

	<-server.Context().Done()
	var remote *quic.ApplicationError
	require.ErrorAs(t, context.Cause(server.Context()), &remote)
	assert.Equal(t, quic.ApplicationError{Remote: true, ErrorCode: 42, ErrorMessage: "done"}, *remote)
	_, err = accepted.Read(make([]byte, 16))
	assert.ErrorAs(t, err, &remote)
	_, err = server.AcceptUniStream(ctx)
	assert.ErrorAs(t, err, &remote)
	<-accepted.Context().Done()
}

func TestConn_idleLoop_Timeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := net.Pipe()
		// The shorter of the two timeouts applies to both ends.
		client, err := Dial(a, &Config{MaxIdleTimeout: 50 * time.Millisecond})
		require.NoError(t, err)
		server, err := Server(b, &Config{MaxIdleTimeout: time.Hour})
		require.NoError(t, err)

		start := time.Now()
		<-client.Context().Done()
		<-server.Context().Done()
		assert.Equal(t, 50*time.Millisecond, time.Since(start))

		// Whichever end times out first shuts the transport down under
		// the other, which may see that before its own timer.
		var idle *quic.IdleTimeoutError
		assert.True(t,
			errors.As(context.Cause(client.Context()), &idle) || errors.As(context.Cause(server.Context()), &idle),
			"neither end reported an idle timeout")
		client.loops.Wait()
		server.loops.Wait()
	})
}

func TestConn_keepAliveLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := net.Pipe()
		client, err := Dial(a, &Config{MaxIdleTimeout: 200 * time.Millisecond, KeepAlivePeriod: 20 * time.Millisecond})
		require.NoError(t, err)
		server, err := Server(b, &Config{MaxIdleTimeout: 200 * time.Millisecond})
		require.NoError(t, err)

		// Three times the idle timeout, with nothing but pings.
		time.Sleep(600 * time.Millisecond)
		synctest.Wait()
		require.NoError(t, context.Cause(client.Context()), "connection closed despite keep-alive")
		require.NoError(t, context.Cause(server.Context()), "connection closed despite keep-alive")

		// The transport parameters, then a ping every 20ms, each answered.
		stats := client.ConnectionStats()
		assert.Equal(t, uint64(31), stats.PacketsSent)
		assert.Equal(t, uint64(31), stats.PacketsReceived)

		require.NoError(t, client.Close())
		require.NoError(t, server.Close())
		client.loops.Wait()
		server.loops.Wait()
	})
}

func TestRTTStats_update(t *testing.T) {
	var s rttStats

	s.update(100 * time.Millisecond)
	assert.Equal(t, rttStats{min: 100 * time.Millisecond, latest: 100 * time.Millisecond, smoothed: 100 * time.Millisecond, deviation: 50 * time.Millisecond}, s)

	s.update(20 * time.Millisecond)
	assert.Equal(t, rttStats{min: 20 * time.Millisecond, latest: 20 * time.Millisecond, smoothed: 90 * time.Millisecond, deviation: 57500 * time.Microsecond}, s)
}

func TestConn_SendDatagram(t *testing.T) {
	config := &Config{EnableDatagrams: true}
	client, server := newTestPair(t, config, config)
	ctx := testContext(t)

	payload := []byte("unreliable in name only")
	require.NoError(t, client.SendDatagram(payload))
	payload[0] = 'X' // the caller's buffer is its own again

	got, err := server.ReceiveDatagram(ctx)
	require.NoError(t, err)
	assert.Equal(t, []byte("unreliable in name only"), got)

	var tooLarge *quic.DatagramTooLargeError
	require.ErrorAs(t, client.SendDatagram(make([]byte, 1200)), &tooLarge)
	assert.Equal(t, int64(1197), tooLarge.MaxDatagramPayloadSize)
	assert.NoError(t, client.SendDatagram(make([]byte, 1197)))
	state := client.ConnectionState()
	assert.True(t, state.SupportsDatagrams.Local)
	assert.True(t, state.SupportsDatagrams.Remote)
}

func TestConn_SendDatagram_NotSupported(t *testing.T) {
	client, _ := newTestPair(t, &Config{EnableDatagrams: true}, nil)

	assert.ErrorIs(t, client.SendDatagram([]byte("x")), ErrDatagramsNotSupported)
}

// rawPeer is the far end of a connection under test, driven by hand.
type rawPeer struct {
	conn    net.Conn
	records chan []byte // the Frames field of each record the connection sent
}

// newRawPeer starts a server connection over an in-memory pipe and returns
// the other end.
func newRawPeer(tb testing.TB, config *Config) (*Conn, *rawPeer) {
	tb.Helper()
	local, remote := net.Pipe()
	server, err := Server(local, config)
	require.NoError(tb, err)
	tb.Cleanup(func() {
		assert.NoError(tb, server.Close())
		assert.NoError(tb, remote.Close())
		server.loops.Wait()
	})

	p := &rawPeer{conn: remote, records: make(chan []byte, 16)}
	go func() {
		defer close(p.records)
		for {
			var size [8]byte
			if _, err := io.ReadFull(remote, size[:1]); err != nil {
				return
			}
			n := 1 << (size[0] >> 6)
			if _, err := io.ReadFull(remote, size[1:n]); err != nil {
				return
			}
			length, _, err := quicvarint.Parse(size[:n])
			if err != nil {
				return
			}
			rec := make([]byte, length)
			if _, err := io.ReadFull(remote, rec); err != nil {
				return
			}
			p.records <- rec
		}
	}()
	return server, p
}

// write sends frames as one record.
func (p *rawPeer) write(tb testing.TB, frames []byte) {
	tb.Helper()
	_, err := p.conn.Write(append(quicvarint.Append(nil, uint64(len(frames))), frames...))
	require.NoError(tb, err)
}

// next returns the first frame of the next record.
func (p *rawPeer) next(tb testing.TB) any {
	tb.Helper()
	select {
	case rec, ok := <-p.records:
		require.True(tb, ok, "connection ended without another record")
		f, _, err := wire.Parse(rec)
		require.NoError(tb, err)
		return f
	case <-time.After(testTimeout):
		require.FailNow(tb, "no record from the connection")
		return nil
	}
}

func TestConn_handleRecord_ProtocolError(t *testing.T) {
	params := (&wire.TransportParameters{Parameters: wire.Parameters{
		InitialMaxData:                 1 << 20,
		InitialMaxStreamDataBidiRemote: 1 << 20,
		InitialMaxStreamsBidi:          10,
	}}).Append(nil)
	frames := func(fs ...wire.Frame) []byte {
		var b []byte
		for _, f := range fs {
			b = f.Append(b)
		}
		return b
	}

	tests := map[string]struct {
		// records are sent after the transport parameters, unless raw.
		records [][]byte
		raw     bool
		code    uint64
	}{
		"first frame is not the transport parameters": {
			records: [][]byte{frames(&wire.MaxData{Max: 1})}, raw: true, code: wire.ProtocolViolation,
		},
		"transport parameters sent twice": {
			records: [][]byte{params}, code: wire.ProtocolViolation,
		},
		"prohibited transport parameter": {
			records: [][]byte{append(append([]byte(nil), params[:8]...), 0x03, 0x0e, 0x01, 0x02)}, raw: true,
			code: wire.TransportParameterError,
		},
		"prohibited frame": {
			records: [][]byte{{0x01}}, code: wire.FrameEncodingError,
		},
		"truncated frame": {
			records: [][]byte{{0x04, 0x02}}, code: wire.FrameEncodingError,
		},
		"record larger than max_record_size": {
			records: [][]byte{make([]byte, wire.DefaultMaxRecordSize+1)}, code: wire.FrameEncodingError,
		},
		"stream beyond the stream limit": {
			records: [][]byte{frames(&wire.Stream{StreamID: 2 * 4, Data: []byte("x")})}, code: wire.StreamLimitError,
		},
		"stream data beyond the stream window": {
			records: [][]byte{
				frames(&wire.Stream{StreamID: 0, Data: make([]byte, 1024)}),
				frames(&wire.Stream{StreamID: 0, Offset: 1024, Data: make([]byte, 1)}),
			},
			code: wire.FlowControlError,
		},
		"stream data beyond the connection window": {
			records: [][]byte{
				frames(&wire.Stream{StreamID: 0, Data: make([]byte, 1024)}),
				frames(&wire.Stream{StreamID: 4, Data: make([]byte, 1024)}),
				frames(&wire.Stream{StreamID: 2, Data: make([]byte, 1)}),
			},
			code: wire.FlowControlError,
		},
		"stream data out of order": {
			records: [][]byte{frames(&wire.Stream{StreamID: 0, Offset: 5, Data: []byte("x")})}, code: wire.ProtocolViolation,
		},
		"stream data after the end of the stream": {
			records: [][]byte{frames(
				&wire.Stream{StreamID: 0, Data: []byte("x"), Fin: true},
				&wire.Stream{StreamID: 0, Offset: 1, Data: []byte("y")},
			)},
			code: wire.FinalSizeError,
		},
		"reset below the received size": {
			records: [][]byte{frames(
				&wire.Stream{StreamID: 0, Data: []byte("xyz")},
				&wire.ResetStream{StreamID: 0, FinalSize: 1},
			)},
			code: wire.FinalSizeError,
		},
		"reset beyond the stream window": {
			records: [][]byte{frames(&wire.ResetStream{StreamID: 0, FinalSize: 1025})}, code: wire.FlowControlError,
		},
		"frame for a stream that was not opened": {
			records: [][]byte{frames(&wire.MaxStreamData{StreamID: 1, Max: 10})}, code: wire.StreamStateError,
		},
		"stream data on a send-only stream": {
			records: [][]byte{frames(&wire.Stream{StreamID: 3, Data: []byte("x")})}, code: wire.StreamStateError,
		},
		"flow control frame for a receive-only stream": {
			records: [][]byte{frames(&wire.MaxStreamData{StreamID: 2, Max: 10})}, code: wire.StreamStateError,
		},
		"ping response without a request": {
			records: [][]byte{frames(&wire.Ping{Response: true, Sequence: 0})}, code: wire.ProtocolViolation,
		},
		"ping sequence number repeated": {
			records: [][]byte{frames(&wire.Ping{Sequence: 4}, &wire.Ping{Sequence: 4})}, code: wire.ProtocolViolation,
		},
		"datagram when not enabled": {
			records: [][]byte{frames(&wire.Datagram{Data: []byte("x")})}, code: wire.ProtocolViolation,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server, peer := newRawPeer(t, &Config{
				MaxIncomingStreams:             2,
				MaxIncomingUniStreams:          2,
				InitialStreamReceiveWindow:     1024,
				InitialConnectionReceiveWindow: 2048,
			})
			require.IsType(t, &wire.TransportParameters{}, peer.next(t))

			if !tt.raw {
				peer.write(t, params)
			}
			for _, rec := range tt.records {
				peer.write(t, rec)
			}

			closeFrame, ok := peer.next(t).(*wire.ConnectionClose)
			require.True(t, ok, "expected CONNECTION_CLOSE")
			assert.False(t, closeFrame.Application)
			assert.Equal(t, tt.code, closeFrame.Code)

			<-server.Context().Done()
			var terr *quic.TransportError
			require.ErrorAs(t, context.Cause(server.Context()), &terr)
			assert.Equal(t, quic.TransportErrorCode(tt.code), terr.ErrorCode)
			assert.False(t, terr.Remote)
		})
	}
}

func TestConn_localParameters(t *testing.T) {
	_, peer := newRawPeer(t, &Config{
		MaxIncomingStreams:             3,
		MaxIncomingUniStreams:          -1,
		InitialStreamReceiveWindow:     1000,
		InitialConnectionReceiveWindow: 2000,
		MaxRecordSize:                  20000,
		MaxIdleTimeout:                 5 * time.Second,
		EnableDatagrams:                true,
	})

	assert.Equal(t, &wire.TransportParameters{Parameters: wire.Parameters{
		MaxIdleTimeout:                 5000,
		InitialMaxData:                 2000,
		InitialMaxStreamDataBidiLocal:  1000,
		InitialMaxStreamDataBidiRemote: 1000,
		InitialMaxStreamDataUni:        1000,
		InitialMaxStreamsBidi:          3,
		MaxDatagramFrameSize:           1200,
		MaxRecordSize:                  20000,
	}}, peer.next(t))
}

// The peer's CONNECTION_CLOSE of the transport kind is reported as such.
func TestConn_handleRecord_PeerTransportClose(t *testing.T) {
	server, peer := newRawPeer(t, nil)
	require.IsType(t, &wire.TransportParameters{}, peer.next(t))

	peer.write(t, (&wire.TransportParameters{}).Append(nil))
	peer.write(t, (&wire.ConnectionClose{Code: wire.InternalError, Reason: "oops"}).Append(nil))

	<-server.Context().Done()
	var terr *quic.TransportError
	require.ErrorAs(t, context.Cause(server.Context()), &terr)
	assert.True(t, terr.Remote)
	assert.Equal(t, InternalError, terr.ErrorCode)
	assert.Equal(t, "oops", terr.ErrorMessage)
}

// Over a message transport a record is one message, without its Size field.
func TestServerMessages(t *testing.T) {
	mc := &fakeMessageConn{in: make(chan []byte, 8), out: make(chan []byte, 8)}
	server, err := ServerMessages(mc, nil)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, server.Close())
		server.loops.Wait()
	}()
	ctx := testContext(t)

	first, _, err := wire.Parse(<-mc.out)
	require.NoError(t, err)
	require.IsType(t, &wire.TransportParameters{}, first)

	mc.in <- (&wire.TransportParameters{Parameters: wire.Parameters{
		InitialMaxData:          1 << 20,
		InitialMaxStreamDataUni: 1 << 20,
		InitialMaxStreamsUni:    1,
	}}).Append(nil)
	// A STREAM frame without a length runs to the end of the message.
	mc.in <- []byte{0x09, 0x02, 'h', 'i'}

	r, err := server.AcceptUniStream(ctx)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, []byte("hi"), got)

	s, err := server.OpenUniStreamSync(ctx)
	require.NoError(t, err)
	_, err = s.Write([]byte("yo"))
	require.NoError(t, err)

	var sent any
	for sent == nil {
		select {
		case msg := <-mc.out:
			f, _, err := wire.Parse(msg)
			require.NoError(t, err)
			if _, ok := f.(*wire.Stream); ok {
				sent = f
			}
		case <-ctx.Done():
			require.FailNow(t, "no STREAM frame from the connection")
		}
	}
	assert.Equal(t, &wire.Stream{StreamID: 3, Data: []byte("yo")}, sent)
	assert.Equal(t, unknownAddr{}, server.RemoteAddr())
}

func TestConfig_normalized(t *testing.T) {
	tests := map[string]struct {
		config   *Config
		expected Config
	}{
		"nil": {
			config: nil,
			expected: Config{
				MaxIncomingStreams: 100, MaxIncomingUniStreams: 100,
				InitialStreamReceiveWindow: 512 * 1024, InitialConnectionReceiveWindow: 1024 * 1024,
				MaxRecordSize: 16382, MaxIdleTimeout: 30 * time.Second,
				HandshakeIdleTimeout: 5 * time.Second,
			},
		},
		"keep-alive longer than half the idle timeout": {
			config: &Config{MaxIdleTimeout: 10 * time.Second, KeepAlivePeriod: 8 * time.Second, HandshakeIdleTimeout: time.Second},
			expected: Config{
				MaxIncomingStreams: 100, MaxIncomingUniStreams: 100,
				InitialStreamReceiveWindow: 512 * 1024, InitialConnectionReceiveWindow: 1024 * 1024,
				MaxRecordSize: 16382, MaxIdleTimeout: 10 * time.Second,
				KeepAlivePeriod: 5 * time.Second, HandshakeIdleTimeout: time.Second,
			},
		},
		"limits out of range": {
			config: &Config{
				MaxIncomingStreams: -1, MaxIncomingUniStreams: 1 << 62,
				InitialStreamReceiveWindow: 1, InitialConnectionReceiveWindow: 2,
				MaxRecordSize: 100, MaxIdleTimeout: -1, MaxDatagramFrameSize: 5000,
			},
			expected: Config{
				MaxIncomingStreams: 0, MaxIncomingUniStreams: 1 << 60,
				InitialStreamReceiveWindow: 1, InitialConnectionReceiveWindow: 2,
				MaxRecordSize: 16382, MaxIdleTimeout: 0,
				HandshakeIdleTimeout: 5 * time.Second,
			},
		},
		"windows beyond a variable-length integer": {
			config: &Config{
				InitialStreamReceiveWindow: math.MaxUint64, InitialConnectionReceiveWindow: math.MaxUint64,
				MaxRecordSize: math.MaxUint64,
			},
			expected: Config{
				MaxIncomingStreams: 100, MaxIncomingUniStreams: 100,
				InitialStreamReceiveWindow: 1<<62 - 1, InitialConnectionReceiveWindow: 1<<62 - 1,
				MaxRecordSize: 1<<62 - 1, MaxIdleTimeout: 30 * time.Second,
				HandshakeIdleTimeout: 5 * time.Second,
			},
		},
		"datagrams": {
			config: &Config{EnableDatagrams: true, MaxDatagramFrameSize: 1 << 20, MaxRecordSize: 20000},
			expected: Config{
				MaxIncomingStreams: 100, MaxIncomingUniStreams: 100,
				InitialStreamReceiveWindow: 512 * 1024, InitialConnectionReceiveWindow: 1024 * 1024,
				MaxRecordSize: 20000, MaxIdleTimeout: 30 * time.Second,
				HandshakeIdleTimeout: 5 * time.Second,
				EnableDatagrams:      true, MaxDatagramFrameSize: 20000,
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.normalized())
		})
	}
}

// A reader that never empties the buffer must not make it grow past the
// flow control window.
func TestStream_Read_BufferBounded(t *testing.T) {
	const window = 16 * 1024
	config := &Config{InitialStreamReceiveWindow: window}
	client, server := newTestPair(t, config, config)
	ctx := testContext(t)

	s, err := client.OpenUniStreamSync(ctx)
	require.NoError(t, err)
	go func() {
		if _, err := s.Write(make([]byte, 2<<20)); err != nil {
			return
		}
		assert.NoError(t, s.Close())
	}()

	r, err := server.AcceptUniStream(ctx)
	require.NoError(t, err)
	largest, total := 0, 0
	buf := make([]byte, 512)
	for {
		n, err := r.Read(buf)
		total += n
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		server.mu.Lock()
		largest = max(largest, cap(r.s.rbuf))
		server.mu.Unlock()
	}
	assert.Equal(t, 2<<20, total)
	assert.LessOrEqual(t, largest, 2*window)
}

// Pings that go unanswered must not keep a connection to a dead peer open:
// sending them resets the idle timer.
func TestConn_keepAliveLoop_DeadPeer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := net.Pipe()
		client, err := Dial(a, &Config{MaxIdleTimeout: 100 * time.Millisecond, KeepAlivePeriod: 20 * time.Millisecond})
		require.NoError(t, err)

		// The peer sends its transport parameters, then reads and never
		// answers.
		go func() {
			params := (&wire.TransportParameters{}).Append(nil)
			if _, err := b.Write(append(quicvarint.Append(nil, uint64(len(params))), params...)); err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, b) // not actionable: ends when the connection closes the pipe
		}()

		start := time.Now()
		<-client.Context().Done()
		var idle *quic.IdleTimeoutError
		assert.ErrorAs(t, context.Cause(client.Context()), &idle)
		// The first ping goes out at 20ms and is 100ms old at 120ms.
		assert.Equal(t, 120*time.Millisecond, time.Since(start))
		client.loops.Wait()
	})
}

// A DATAGRAM frame without a length may be as large as the limit.
func TestConn_ReceiveDatagram_WithoutLength(t *testing.T) {
	server, peer := newRawPeer(t, &Config{EnableDatagrams: true})
	require.IsType(t, &wire.TransportParameters{}, peer.next(t))

	peer.write(t, (&wire.TransportParameters{}).Append(nil))
	peer.write(t, append([]byte{0x30}, make([]byte, 1199)...))

	got, err := server.ReceiveDatagram(testContext(t))
	require.NoError(t, err)
	assert.Len(t, got, 1199)
}

// An idle timeout too long for a time.Duration leaves the local one alone.
func TestConn_handleRecord_HugeIdleTimeout(t *testing.T) {
	server, peer := newRawPeer(t, &Config{MaxIdleTimeout: time.Minute})
	require.IsType(t, &wire.TransportParameters{}, peer.next(t))

	peer.write(t, (&wire.TransportParameters{Parameters: wire.Parameters{MaxIdleTimeout: 1<<62 - 1}}).Append(nil))

	<-server.handshake
	assert.Equal(t, time.Minute, server.currentIdleTimeout())
}

// A reason too long for a record is cut, so that the peer still gets the
// error code.
func TestConn_CloseWithError_LongReason(t *testing.T) {
	client, server := newTestPair(t, nil, nil)

	require.NoError(t, client.CloseWithError(3, strings.Repeat("x", 40000)))

	<-server.Context().Done()
	var remote *quic.ApplicationError
	require.ErrorAs(t, context.Cause(server.Context()), &remote)
	assert.Equal(t, ApplicationErrorCode(3), remote.ErrorCode)
	assert.Len(t, remote.ErrorMessage, maxReasonLength)
}

func TestReceiveStream_Peek(t *testing.T) {
	config := &Config{InitialStreamReceiveWindow: 1024}
	client, server := newTestPair(t, config, config)
	ctx := testContext(t)

	s, err := client.OpenUniStreamSync(ctx)
	require.NoError(t, err)
	_, err = s.Write([]byte("hello"))
	require.NoError(t, err)

	r, err := server.AcceptUniStream(ctx)
	require.NoError(t, err)

	// Peeking waits for all of the bytes asked for, and consumes none.
	peeked := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 8)
		n, err := r.Peek(buf)
		if err != nil {
			return
		}
		peeked <- buf[:n]
	}()
	_, err = s.Write([]byte(" world"))
	require.NoError(t, err)
	select {
	case got := <-peeked:
		assert.Equal(t, []byte("hello wo"), got)
	case <-ctx.Done():
		require.FailNow(t, "Peek did not return")
	}

	// At the end of the stream it returns what there is.
	require.NoError(t, s.Close())
	buf := make([]byte, 64)
	n, err := r.Peek(buf)
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, []byte("hello world"), buf[:n])

	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, []byte("hello world"), got)

	_, err = r.Peek(make([]byte, 1025))
	assert.Error(t, err, "a peek larger than the window can never be filled")
}

func TestReceiveStream_Peek_Deadline(t *testing.T) {
	client, server := newTestPair(t, nil, nil)
	ctx := testContext(t)

	s, err := client.OpenUniStreamSync(ctx)
	require.NoError(t, err)
	_, err = s.Write([]byte("hi"))
	require.NoError(t, err)
	r, err := server.AcceptUniStream(ctx)
	require.NoError(t, err)

	require.NoError(t, r.SetReadDeadline(time.Now().Add(20*time.Millisecond)))
	_, err = r.Peek(make([]byte, 3))
	assert.ErrorIs(t, err, os.ErrDeadlineExceeded)
}

func TestConn_HandshakeComplete(t *testing.T) {
	server, peer := newRawPeer(t, nil)
	require.IsType(t, &wire.TransportParameters{}, peer.next(t))

	select {
	case <-server.HandshakeComplete():
		require.FailNow(t, "handshake complete before the peer's transport parameters")
	default:
	}

	peer.write(t, (&wire.TransportParameters{}).Append(nil))
	select {
	case <-server.HandshakeComplete():
	case <-time.After(testTimeout):
		require.FailNow(t, "handshake did not complete")
	}
	assert.NoError(t, context.Cause(server.Context()))
}

// A peer that never sends its transport parameters is not waited for.
func TestConn_idleLoop_HandshakeTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := net.Pipe()
		client, err := Dial(a, &Config{HandshakeIdleTimeout: 2 * time.Second})
		require.NoError(t, err)
		go func() {
			_, _ = io.Copy(io.Discard, b) // not actionable: ends when the connection closes the pipe
		}()

		start := time.Now()
		<-client.HandshakeComplete()
		assert.Equal(t, 2*time.Second, time.Since(start))

		var timeout *quic.HandshakeTimeoutError
		assert.ErrorAs(t, context.Cause(client.Context()), &timeout)
		_, err = client.OpenStream()
		assert.ErrorAs(t, err, &timeout)
		assert.ErrorAs(t, client.SendDatagram([]byte("x")), &timeout)
		client.loops.Wait()
	})
}

func TestSendStream_SetPriority(t *testing.T) {
	client, _ := newTestPair(t, nil, nil)

	s, err := client.OpenUniStreamSync(testContext(t))
	require.NoError(t, err)
	assert.Equal(t, priority{urgency: 3, incremental: true, streamID: s.StreamID()}, s.s.priority())

	tests := map[string]struct {
		urgency     int8
		incremental bool
		expected    priority
	}{
		"within range":  {urgency: 1, incremental: false, expected: priority{urgency: 1}},
		"below range":   {urgency: -5, incremental: true, expected: priority{urgency: 0, incremental: true}},
		"above range":   {urgency: 100, incremental: true, expected: priority{urgency: 7, incremental: true}},
		"lowest urgent": {urgency: 7, incremental: false, expected: priority{urgency: 7}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s.SetPriority(tt.urgency, tt.incremental)
			tt.expected.streamID = s.StreamID()
			assert.Equal(t, tt.expected, s.s.priority())
		})
	}
}

func TestConfig_Clone(t *testing.T) {
	config := &Config{MaxIncomingStreams: 7, EnableDatagrams: true}

	clone := config.Clone()
	assert.Equal(t, config, clone)
	clone.MaxIncomingStreams = 8
	assert.Equal(t, int64(7), config.MaxIncomingStreams)

	assert.Nil(t, (*Config)(nil).Clone())
}
