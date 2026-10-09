package qmux

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/okdaichi/qmux-go/qmux/internal/wire"
	"github.com/quic-go/quic-go"
)

const (
	dirBidi = 0
	dirUni  = 1

	// maxControlFrameSize bounds the encoding of every frame that goes
	// through the control queue.
	maxControlFrameSize = 32

	datagramQueueSize = 100
)

// signal wakes the goroutines waiting for a state change. The connection's
// mutex guards it.
type signal struct {
	ch chan struct{}
}

// wait returns a channel that the next broadcast closes.
func (s *signal) wait() <-chan struct{} {
	if s.ch == nil {
		s.ch = make(chan struct{})
	}
	return s.ch
}

func (s *signal) broadcast() {
	if s.ch != nil {
		close(s.ch)
		s.ch = nil
	}
}

// outgoing is the state of the streams this endpoint opens, of one
// direction kind.
type outgoing struct {
	next   uint64 // index of the next stream to open
	max    uint64 // number of streams the peer allows, cumulative
	signal signal // broadcast when max grows
}

// incoming is the state of the streams the peer opens, of one direction
// kind.
type incoming struct {
	next    uint64 // index of the next stream the peer has not opened yet
	max     uint64 // number of streams the peer may open, cumulative
	pending bool   // max has grown since the last MAX_STREAMS
	queue   []*stream
	signal  signal // broadcast when queue grows
}

// Conn is a QMux connection: QUIC's streams and datagrams over a reliable,
// ordered transport. Its methods mirror those of a quic-go connection, and
// are safe for concurrent use.
//
// A connection runs goroutines of its own from Dial or Server on. Close and
// CloseWithError stop them without waiting: they end once the peer has shut
// down in turn, or a second later.
type Conn struct {
	tr       recordTransport
	config   Config
	isServer bool

	ctx    context.Context
	cancel context.CancelCauseFunc
	// loops counts the connection's goroutines. They all end with ctx.
	loops sync.WaitGroup

	// handshake is closed once the peer's transport parameters are in.
	handshake chan struct{}

	// writeMu serializes records on the transport. It is taken before mu,
	// and never by the goroutine that reads the transport. A stream holds
	// it from reserving flow control credit to writing the data, so that
	// what follows in the control queue (a RESET_STREAM, say) follows on
	// the wire too.
	writeMu sync.Mutex
	wbuf    []byte // record under construction; guarded by writeMu

	mu       sync.Mutex
	closeErr error
	peer     wire.Parameters
	ready    bool // the peer's transport parameters are in
	streams  map[StreamID]*stream
	out      [2]outgoing
	in       [2]incoming

	// Connection-level flow control.
	sendMax    uint64 // the peer's limit on the data this endpoint sends
	sent       uint64
	sendSignal signal // broadcast when sendMax grows
	recvMax    uint64 // the limit declared to the peer
	received   uint64
	consumed   uint64 // read by the application, or discarded

	// Control frames waiting for the write loop. Connection-level limits
	// and pings are kept as flags, so that a peer cannot grow the queue by
	// provoking them.
	control        []wire.Frame
	maxDataPending bool
	pongPending    bool
	pongSequence   uint64
	pingPending    bool
	controlWake    chan struct{}

	idleTimeout  time.Duration
	nextPing     uint64 // sequence number of the next QX_PING request
	pingSentAt   time.Time
	peerPinged   bool
	peerPingLast uint64
	rtt          rttStats

	datagrams    chan []byte
	lastActivity atomic.Int64 // UnixNano of the last record sent or received

	bytesSent       atomic.Uint64
	bytesReceived   atomic.Uint64
	recordsSent     atomic.Uint64
	recordsReceived atomic.Uint64
}

type rttStats struct {
	min, latest, smoothed, deviation time.Duration
}

func newConn(tr recordTransport, config Config, isServer bool) *Conn {
	ctx, cancel := context.WithCancelCause(context.Background())
	c := &Conn{
		tr:          tr,
		config:      config,
		isServer:    isServer,
		ctx:         ctx,
		cancel:      cancel,
		handshake:   make(chan struct{}),
		wbuf:        make([]byte, 0, recordHeadroom+wire.DefaultMaxRecordSize),
		streams:     make(map[StreamID]*stream),
		recvMax:     config.InitialConnectionReceiveWindow,
		controlWake: make(chan struct{}, 1),
		idleTimeout: config.MaxIdleTimeout,
		datagrams:   make(chan []byte, datagramQueueSize),
	}
	c.in[dirBidi].max = uint64(config.MaxIncomingStreams)
	c.in[dirUni].max = uint64(config.MaxIncomingUniStreams)
	c.touch()

	// The transport parameters have to be the first frame on the wire.
	// The lock is handed to writeLoop, which releases it once it has
	// written them.
	c.writeMu.Lock()
	c.loops.Go(c.writeLoop)
	c.loops.Go(c.readLoop)
	c.loops.Go(c.idleLoop)
	if config.KeepAlivePeriod > 0 {
		c.loops.Go(c.keepAliveLoop)
	}
	return c
}

func (c *Conn) localParameters() wire.Parameters {
	p := wire.Parameters{
		MaxIdleTimeout:                 uint64(c.config.MaxIdleTimeout / time.Millisecond),
		InitialMaxData:                 c.config.InitialConnectionReceiveWindow,
		InitialMaxStreamDataBidiLocal:  c.config.InitialStreamReceiveWindow,
		InitialMaxStreamDataBidiRemote: c.config.InitialStreamReceiveWindow,
		InitialMaxStreamDataUni:        c.config.InitialStreamReceiveWindow,
		InitialMaxStreamsBidi:          uint64(c.config.MaxIncomingStreams),
		InitialMaxStreamsUni:           uint64(c.config.MaxIncomingUniStreams),
		MaxDatagramFrameSize:           c.config.MaxDatagramFrameSize,
	}
	if c.config.MaxRecordSize != wire.DefaultMaxRecordSize {
		p.MaxRecordSize = c.config.MaxRecordSize
	}
	return p
}

func (c *Conn) touch() {
	c.lastActivity.Store(time.Now().UnixNano())
}

// sendParameters writes the transport parameters and releases writeMu,
// which newConn took on its behalf.
func (c *Conn) sendParameters() error {
	defer c.writeMu.Unlock()
	return c.writeFramesLocked(&wire.TransportParameters{Parameters: c.localParameters()})
}

// writeLoop sends the transport parameters, then the control frames as they
// are queued. It ends with the connection.
func (c *Conn) writeLoop() {
	err := c.sendParameters()
	for err == nil {
		select {
		case <-c.controlWake:
			err = c.flushControl()
		case <-c.ctx.Done():
			return
		}
	}
	c.abort(fmt.Errorf("qmux: write to transport: %w", err))
}

// writeFrames writes the frames as one record.
func (c *Conn) writeFrames(frames ...wire.Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.writeFramesLocked(frames...)
}

// writeFramesLocked writes the frames as one record. The caller holds
// writeMu.
func (c *Conn) writeFramesLocked(frames ...wire.Frame) error {
	buf := c.wbuf[:recordHeadroom]
	for _, f := range frames {
		buf = f.Append(buf)
	}
	return c.writeRecordLocked(buf)
}

// writeRecordLocked writes buf, a record built on wbuf. The caller holds
// writeMu.
func (c *Conn) writeRecordLocked(buf []byte) error {
	c.wbuf = buf[:0]
	n, err := c.tr.writeRecord(buf)
	if err != nil {
		return err
	}
	c.bytesSent.Add(uint64(n))
	c.recordsSent.Add(1)
	c.touch()
	return nil
}

func (c *Conn) flushControl() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	const full = recordHeadroom + wire.DefaultMaxRecordSize - maxControlFrameSize
	buf := c.wbuf[:recordHeadroom]
	for _, f := range c.takeControl() {
		buf = f.Append(buf)
		if len(buf) > full {
			if err := c.writeRecordLocked(buf); err != nil {
				return err
			}
			buf = c.wbuf[:recordHeadroom]
		}
	}
	if len(buf) == recordHeadroom {
		return nil
	}
	return c.writeRecordLocked(buf)
}

// takeControl empties the control queue. A closed connection sends nothing
// after its CONNECTION_CLOSE frame.
func (c *Conn) takeControl() []wire.Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeErr != nil {
		return nil
	}
	frames := c.control
	c.control = nil
	if c.maxDataPending {
		c.maxDataPending = false
		frames = append(frames, &wire.MaxData{Max: c.recvMax})
	}
	for dir := range c.in {
		if in := &c.in[dir]; in.pending {
			in.pending = false
			frames = append(frames, &wire.MaxStreams{Uni: dir == dirUni, Max: in.max})
		}
	}
	if c.pongPending {
		c.pongPending = false
		frames = append(frames, &wire.Ping{Response: true, Sequence: c.pongSequence})
	}
	if c.pingPending {
		c.pingPending = false
		frames = append(frames, &wire.Ping{Sequence: c.nextPing})
		c.nextPing++
		c.pingSentAt = time.Now()
	}
	return frames
}

// queueLocked hands a frame to the write loop.
func (c *Conn) queueLocked(f wire.Frame) {
	c.control = append(c.control, f)
	c.wakeWriter()
}

func (c *Conn) wakeWriter() {
	select {
	case c.controlWake <- struct{}{}:
	default:
	}
}

// readLoop reads records until the transport ends. It never waits for the
// application, so that the frames that keep the connection going are always
// processed (Section 6). A connection that has closed keeps reading, and
// discards, until the peer shuts down in turn.
func (c *Conn) readLoop() {
	defer func() {
		_ = c.tr.close() // not actionable: the connection has ended
	}()
	for {
		rec, err := c.tr.readRecord()
		if err != nil {
			var werr *wire.Error
			if errors.As(err, &werr) {
				// A record over the limit: the transport skipped it.
				c.closeWithTransportError(werr)
				continue
			}
			c.abort(fmt.Errorf("qmux: transport closed: %w", err))
			return
		}
		c.bytesReceived.Add(uint64(len(rec)))
		c.recordsReceived.Add(1)
		c.touch()

		err = c.handleRecord(rec)
		var werr *wire.Error
		switch {
		case err == nil:
		case errors.As(err, &werr):
			c.closeWithTransportError(werr)
		default:
			// The peer closed the connection.
			c.abort(err)
			return
		}
	}
}

func (c *Conn) idleLoop() {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	handshake := c.handshake
	for {
		var expired <-chan time.Time
		if timeout := c.currentIdleTimeout(); timeout > 0 {
			remaining := timeout - time.Since(time.Unix(0, c.lastActivity.Load()))
			if remaining <= 0 {
				// An idle connection closes without a frame (Section 7.2).
				c.closeGracefully(&quic.IdleTimeoutError{}, nil)
				return
			}
			timer.Reset(remaining)
			expired = timer.C
		}
		select {
		case <-expired:
		case <-handshake:
			// The peer's parameters may have shortened the timeout.
			handshake = nil
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Conn) currentIdleTimeout() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.idleTimeout
}

func (c *Conn) requestPing() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pingPending = true
	c.wakeWriter()
}

func (c *Conn) keepAliveLoop() {
	ticker := time.NewTicker(c.config.KeepAlivePeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.requestPing()
		case <-c.ctx.Done():
			return
		}
	}
}

// terminate records why the connection ended and wakes everything that
// waits on it. It reports whether this call ended the connection.
func (c *Conn) terminate(err error) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeErr != nil {
		return false
	}
	c.closeErr = err
	c.cancel(err)
	c.sendSignal.broadcast()
	for dir := range c.out {
		c.out[dir].signal.broadcast()
		c.in[dir].signal.broadcast()
	}
	for _, s := range c.streams {
		s.readSignal.broadcast()
		s.writeSignal.broadcast()
	}
	clear(c.streams)
	return true
}

// abort ends the connection at once, without telling the peer.
func (c *Conn) abort(err error) {
	if c.terminate(err) {
		_ = c.tr.close() // not actionable: the connection has ended
	}
}

// closeGracefully ends the connection: it sends frame, unless nil, shuts
// down the sending side and leaves the read loop to discard what the peer
// still sends until it shuts down in turn (Section 7.2). The caller must
// not hold writeMu.
func (c *Conn) closeGracefully(err error, frame *wire.ConnectionClose) {
	if !c.terminate(err) {
		return
	}
	// A peer that has stopped reading must not hold the close up.
	c.tr.expire(time.Now().Add(closeTimeout))
	forced := time.AfterFunc(closeTimeout, func() {
		_ = c.tr.close() // not actionable: the connection has ended
	})

	var werr error
	if frame != nil {
		werr = c.writeFrames(frame)
	}
	if werr != nil || !c.tr.closeWrite() {
		forced.Stop()
		_ = c.tr.close() // not actionable: the connection has ended
	}
}

func (c *Conn) closeWithTransportError(e *wire.Error) {
	c.closeGracefully(
		&quic.TransportError{ErrorCode: quic.TransportErrorCode(e.Code), ErrorMessage: e.Reason},
		&wire.ConnectionClose{Code: e.Code, Reason: e.Reason},
	)
}

// Close closes the connection with error code 0.
func (c *Conn) Close() error {
	return c.CloseWithError(0, "")
}

// CloseWithError closes the connection with an application error code and a
// reason. Streams and pending operations fail with a *quic.ApplicationError.
func (c *Conn) CloseWithError(code ApplicationErrorCode, msg string) error {
	c.closeGracefully(
		&quic.ApplicationError{ErrorCode: code, ErrorMessage: msg},
		&wire.ConnectionClose{Application: true, Code: uint64(code), Reason: msg},
	)
	return nil
}

func protocolViolation(reason string) *wire.Error {
	return &wire.Error{Code: wire.ProtocolViolation, Reason: reason}
}

// handleRecord processes the frames of a record. It returns a *wire.Error
// for a violation by the peer, and the peer's reason when it closed the
// connection.
func (c *Conn) handleRecord(rec []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(rec) > 0 && c.closeErr == nil {
		f, rest, err := wire.Parse(rec)
		if err != nil {
			return err
		}
		if f == nil {
			return nil
		}
		rec = rest
		if err := c.handleFrameLocked(f); err != nil {
			return err
		}
	}
	return nil
}

func (c *Conn) handleFrameLocked(f any) error {
	// QX_TRANSPORT_PARAMETERS is the first frame, and only the first
	// (Section 4.2).
	if params, ok := f.(*wire.TransportParameters); ok != !c.ready {
		return protocolViolation("QX_TRANSPORT_PARAMETERS must be the first frame, and only the first")
	} else if ok {
		c.handleParametersLocked(params.Parameters)
		return nil
	}

	switch f := f.(type) {
	case *wire.Stream:
		return c.handleStreamLocked(f)
	case *wire.ResetStream:
		return c.handleResetStreamLocked(f)
	case *wire.StopSending:
		s, err := c.streamLocked(f.StreamID, false)
		if s != nil {
			s.stopSendingLocked(StreamErrorCode(f.Code))
		}
		return err
	case *wire.MaxStreamData:
		s, err := c.streamLocked(f.StreamID, false)
		if s != nil && f.Max > s.sendMax {
			s.sendMax = f.Max
			s.writeSignal.broadcast()
		}
		return err
	case *wire.MaxData:
		if f.Max > c.sendMax {
			c.sendMax = f.Max
			c.sendSignal.broadcast()
		}
	case *wire.MaxStreams:
		out := &c.out[dirBidi]
		if f.Uni {
			out = &c.out[dirUni]
		}
		if f.Max > out.max {
			out.max = f.Max
			out.signal.broadcast()
		}
	case *wire.Ping:
		return c.handlePingLocked(f)
	case *wire.Datagram:
		return c.handleDatagramLocked(f)
	case *wire.ConnectionClose:
		if f.Application {
			return &quic.ApplicationError{Remote: true, ErrorCode: ApplicationErrorCode(f.Code), ErrorMessage: f.Reason}
		}
		return &quic.TransportError{Remote: true, ErrorCode: TransportErrorCode(f.Code), FrameType: f.FrameType, ErrorMessage: f.Reason}
	case *wire.Blocked:
		// Informational.
	}
	return nil
}

func (c *Conn) handleParametersLocked(p wire.Parameters) {
	c.peer = p
	c.ready = true
	c.sendMax = p.InitialMaxData
	c.out[dirBidi].max = p.InitialMaxStreamsBidi
	c.out[dirUni].max = p.InitialMaxStreamsUni
	if peerIdle := time.Duration(p.MaxIdleTimeout) * time.Millisecond; peerIdle > 0 {
		if c.idleTimeout == 0 || peerIdle < c.idleTimeout {
			c.idleTimeout = peerIdle
		}
	}
	close(c.handshake)
	c.sendSignal.broadcast()
	for dir := range c.out {
		c.out[dir].signal.broadcast()
	}
}

// streamLocked returns the stream that a frame refers to. recv tells
// whether the frame concerns the data the peer sends on the stream (STREAM,
// RESET_STREAM) or the data this endpoint sends (STOP_SENDING,
// MAX_STREAM_DATA). A peer-initiated stream the frame opens is created,
// with every lower-numbered one. A nil stream with a nil error is a stream
// that has already closed: its frame is ignored.
func (c *Conn) streamLocked(rawID uint64, recv bool) (*stream, error) {
	id := StreamID(rawID)
	local := c.initiatedLocally(id)
	dir := streamDir(id)
	index := rawID >> 2

	if dir == dirUni && local == recv {
		return nil, &wire.Error{
			Code:   wire.StreamStateError,
			Reason: fmt.Sprintf("frame not allowed on unidirectional stream %d", rawID),
		}
	}
	if local {
		if index >= c.out[dir].next {
			return nil, &wire.Error{
				Code:   wire.StreamStateError,
				Reason: fmt.Sprintf("frame for stream %d, which was not opened", rawID),
			}
		}
		return c.streams[id], nil
	}
	in := &c.in[dir]
	if index >= in.max {
		return nil, &wire.Error{
			Code:   wire.StreamLimitError,
			Reason: fmt.Sprintf("stream %d exceeds the stream limit", rawID),
		}
	}
	for in.next <= index {
		s := c.newStreamLocked(c.streamID(in.next, dir, false))
		in.queue = append(in.queue, s)
		in.next++
		in.signal.broadcast()
	}
	return c.streams[id], nil
}

func (c *Conn) handleStreamLocked(f *wire.Stream) error {
	s, err := c.streamLocked(f.StreamID, true)
	if s == nil {
		return err
	}
	n := uint64(len(f.Data))
	if f.Offset != s.recvOffset {
		return protocolViolation(fmt.Sprintf("stream %d: data out of order", f.StreamID))
	}
	if s.finRecv || s.recvReset {
		if n > 0 || (f.Fin && !s.finRecv) {
			return &wire.Error{Code: wire.FinalSizeError, Reason: fmt.Sprintf("stream %d: data past its final size", f.StreamID)}
		}
		return nil
	}
	if s.recvOffset+n > s.recvMax {
		return &wire.Error{Code: wire.FlowControlError, Reason: fmt.Sprintf("stream %d: flow control limit exceeded", f.StreamID)}
	}
	if err := c.receivedLocked(n); err != nil {
		return err
	}
	s.recvOffset += n
	s.finRecv = f.Fin
	if s.readCancelled {
		c.consumeLocked(n)
	} else {
		s.rbuf = append(s.rbuf, f.Data...)
	}
	s.readSignal.broadcast()
	c.completeLocked(s)
	return nil
}

func (c *Conn) handleResetStreamLocked(f *wire.ResetStream) error {
	s, err := c.streamLocked(f.StreamID, true)
	if s == nil {
		return err
	}
	if f.FinalSize < s.recvOffset || ((s.finRecv || s.recvReset) && f.FinalSize != s.recvOffset) {
		return &wire.Error{Code: wire.FinalSizeError, Reason: fmt.Sprintf("stream %d: final size changed", f.StreamID)}
	}
	if s.recvReset {
		return nil
	}
	// The data that was never sent counts against the connection's limit
	// all the same (RFC 9000, Section 4.5).
	skipped := f.FinalSize - s.recvOffset
	if err := c.receivedLocked(skipped); err != nil {
		return err
	}
	c.consumeLocked(skipped + uint64(len(s.rbuf)-s.roff))
	s.recvOffset = f.FinalSize
	s.recvReset = true
	s.rbuf, s.roff = nil, 0
	if s.recvErr == nil {
		s.recvErr = &quic.StreamError{StreamID: s.id, ErrorCode: StreamErrorCode(f.Code), Remote: true}
	}
	s.readSignal.broadcast()
	c.completeLocked(s)
	return nil
}

// receivedLocked accounts for stream data the peer has sent.
func (c *Conn) receivedLocked(n uint64) error {
	if c.received+n > c.recvMax {
		return &wire.Error{Code: wire.FlowControlError, Reason: "connection flow control limit exceeded"}
	}
	c.received += n
	return nil
}

// consumeLocked accounts for stream data that left the receive buffers, and
// extends the connection's limit once half the window is used.
func (c *Conn) consumeLocked(n uint64) {
	c.consumed += n
	window := c.config.InitialConnectionReceiveWindow
	if c.recvMax-c.consumed < window/2 {
		c.recvMax = c.consumed + window
		c.maxDataPending = true
		c.wakeWriter()
	}
}

func (c *Conn) handlePingLocked(f *wire.Ping) error {
	if f.Response {
		if f.Sequence >= c.nextPing {
			return protocolViolation("QX_PING response to a request that was not sent")
		}
		if f.Sequence == c.nextPing-1 {
			c.rtt.update(time.Since(c.pingSentAt))
		}
		return nil
	}
	if c.peerPinged && f.Sequence <= c.peerPingLast {
		return protocolViolation("QX_PING sequence number did not increase")
	}
	c.peerPinged = true
	c.peerPingLast = f.Sequence
	// One response, to the latest request, answers them all (Section 4.3).
	c.pongPending = true
	c.pongSequence = f.Sequence
	c.wakeWriter()
	return nil
}

func (s *rttStats) update(rtt time.Duration) {
	s.latest = rtt
	if s.min == 0 || rtt < s.min {
		s.min = rtt
	}
	if s.smoothed == 0 {
		s.smoothed = rtt
		s.deviation = rtt / 2
		return
	}
	// RFC 9002, Section 5.3.
	s.deviation = (3*s.deviation + (s.smoothed - rtt).Abs()) / 4
	s.smoothed = (7*s.smoothed + rtt) / 8
}

func (c *Conn) handleDatagramLocked(f *wire.Datagram) error {
	if uint64(f.Len()) > c.config.MaxDatagramFrameSize {
		return protocolViolation("DATAGRAM frame larger than max_datagram_frame_size")
	}
	select {
	case c.datagrams <- append([]byte(nil), f.Data...):
	default:
		// The application is not keeping up: drop (Section 6).
	}
	return nil
}

func (c *Conn) streamID(index uint64, dir int, local bool) StreamID {
	id := index << 2
	if dir == dirUni {
		id |= 0x2
	}
	if local == c.isServer {
		id |= 0x1
	}
	return StreamID(id)
}

func (c *Conn) initiatedLocally(id StreamID) bool {
	return (id&0x1 == 0x1) == c.isServer
}

func streamDir(id StreamID) int {
	if id&0x2 != 0 {
		return dirUni
	}
	return dirBidi
}

// completeLocked forgets a stream whose two directions have ended, and
// lets the peer open another in its place. A stream the application has not
// accepted yet stays counted, so that the accept queue cannot outgrow the
// stream limit.
func (c *Conn) completeLocked(s *stream) {
	if s.done || !s.accepted || !s.recvDone() || !s.sendDone() {
		return
	}
	s.done = true
	s.rbuf = nil
	delete(c.streams, s.id)
	if !c.initiatedLocally(s.id) {
		in := &c.in[streamDir(s.id)]
		in.max++
		in.pending = true
		c.wakeWriter()
	}
}

// waitHandshake waits for the peer's transport parameters.
func (c *Conn) waitHandshake(ctx context.Context) error {
	select {
	case <-c.handshake:
		return nil
	case <-c.ctx.Done():
		return context.Cause(c.ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// tryOpen opens a stream if the peer's limit allows it. Otherwise it
// returns a channel that is closed when the limit may have changed.
func (c *Conn) tryOpen(dir int) (*stream, <-chan struct{}, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeErr != nil {
		return nil, nil, c.closeErr
	}
	out := &c.out[dir]
	if out.next >= out.max {
		return nil, out.signal.wait(), nil
	}
	s := c.newStreamLocked(c.streamID(out.next, dir, true))
	s.accepted = true
	out.next++
	return s, nil, nil
}

func (c *Conn) open(ctx context.Context, dir int, block bool) (*stream, error) {
	if err := c.waitHandshake(ctx); err != nil {
		return nil, err
	}
	for {
		s, wake, err := c.tryOpen(dir)
		if s != nil || err != nil {
			return s, err
		}
		if !block {
			return nil, ErrStreamLimitReached
		}
		select {
		case <-wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// tryAccept returns the next stream the peer opened, or a channel that is
// closed when there may be one.
func (c *Conn) tryAccept(dir int) (*stream, <-chan struct{}, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeErr != nil {
		return nil, nil, c.closeErr
	}
	in := &c.in[dir]
	if len(in.queue) == 0 {
		return nil, in.signal.wait(), nil
	}
	s := in.queue[0]
	in.queue[0] = nil
	in.queue = in.queue[1:]
	s.accepted = true
	c.completeLocked(s)
	return s, nil, nil
}

func (c *Conn) accept(ctx context.Context, dir int) (*stream, error) {
	for {
		s, wake, err := c.tryAccept(dir)
		if s != nil || err != nil {
			return s, err
		}
		select {
		case <-wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// AcceptStream returns the next bidirectional stream opened by the peer. A
// stream opens with the first frame sent on it.
func (c *Conn) AcceptStream(ctx context.Context) (*Stream, error) {
	s, err := c.accept(ctx, dirBidi)
	if err != nil {
		return nil, err
	}
	return &Stream{s: s}, nil
}

// AcceptUniStream returns the next unidirectional stream opened by the
// peer.
func (c *Conn) AcceptUniStream(ctx context.Context) (*ReceiveStream, error) {
	s, err := c.accept(ctx, dirUni)
	if err != nil {
		return nil, err
	}
	return &ReceiveStream{s: s}, nil
}

// OpenStream opens a bidirectional stream. It waits for the peer's
// transport parameters, and then fails with ErrStreamLimitReached if the
// peer's stream limit is reached. The peer learns of the stream with the
// first data written to it.
func (c *Conn) OpenStream() (*Stream, error) {
	s, err := c.open(c.ctx, dirBidi, false)
	if err != nil {
		return nil, err
	}
	return &Stream{s: s}, nil
}

// OpenStreamSync opens a bidirectional stream, waiting while the peer's
// stream limit is reached.
func (c *Conn) OpenStreamSync(ctx context.Context) (*Stream, error) {
	s, err := c.open(ctx, dirBidi, true)
	if err != nil {
		return nil, err
	}
	return &Stream{s: s}, nil
}

// OpenUniStream opens a unidirectional stream. It waits for the peer's
// transport parameters, and then fails with ErrStreamLimitReached if the
// peer's stream limit is reached.
func (c *Conn) OpenUniStream() (*SendStream, error) {
	s, err := c.open(c.ctx, dirUni, false)
	if err != nil {
		return nil, err
	}
	return &SendStream{s: s}, nil
}

// OpenUniStreamSync opens a unidirectional stream, waiting while the
// peer's stream limit is reached.
func (c *Conn) OpenUniStreamSync(ctx context.Context) (*SendStream, error) {
	s, err := c.open(ctx, dirUni, true)
	if err != nil {
		return nil, err
	}
	return &SendStream{s: s}, nil
}

// SendDatagram sends a datagram. It fails if the peer does not accept
// datagrams, or none as large as p.
func (c *Conn) SendDatagram(p []byte) error {
	if err := c.waitHandshake(c.ctx); err != nil {
		return err
	}
	f := &wire.Datagram{Data: p}
	limit := c.datagramLimit()
	if limit == 0 {
		return ErrDatagramsNotSupported
	}
	if uint64(f.Len()) > limit {
		return fmt.Errorf("%w: frame of %d bytes, peer accepts %d", ErrDatagramTooLarge, f.Len(), limit)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := context.Cause(c.ctx); err != nil {
		return err
	}
	if err := c.writeFramesLocked(f); err != nil {
		c.abort(fmt.Errorf("qmux: write to transport: %w", err))
		return context.Cause(c.ctx)
	}
	return nil
}

// datagramLimit returns the largest DATAGRAM frame the peer accepts, or
// zero if it accepts none.
func (c *Conn) datagramLimit() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return min(c.peer.MaxDatagramFrameSize, c.peer.MaxRecordSize)
}

// ReceiveDatagram returns the next datagram the peer sent. Datagrams that
// arrive faster than they are received are dropped.
func (c *Conn) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	select {
	case p := <-c.datagrams:
		return p, nil
	case <-c.ctx.Done():
		return nil, context.Cause(c.ctx)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// LocalAddr returns the local address of the transport.
func (c *Conn) LocalAddr() net.Addr { return c.tr.localAddr() }

// RemoteAddr returns the peer's address on the transport.
func (c *Conn) RemoteAddr() net.Addr { return c.tr.remoteAddr() }

// Context returns a context that is cancelled when the connection closes,
// with the reason as its cause.
func (c *Conn) Context() context.Context { return c.ctx }

// ConnectionState reports the state of the connection. Its TLS field is
// that of the transport, when the transport is a TLS connection: QMux
// negotiates no application protocol of its own.
func (c *Conn) ConnectionState() quic.ConnectionState {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := quic.ConnectionState{TLS: c.tr.tlsState()}
	state.SupportsDatagrams.Local = c.config.EnableDatagrams
	state.SupportsDatagrams.Remote = c.peer.MaxDatagramFrameSize > 0
	return state
}

// ConnectionStats returns statistics about the connection. Packets are
// records; the round-trip time is measured from QX_PING frames, and stays
// zero unless Config.KeepAlivePeriod is set.
func (c *Conn) ConnectionStats() quic.ConnectionStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	rtt := c.rtt
	return quic.ConnectionStats{
		MinRTT:          rtt.min,
		LatestRTT:       rtt.latest,
		SmoothedRTT:     rtt.smoothed,
		MeanDeviation:   rtt.deviation,
		BytesSent:       c.bytesSent.Load(),
		PacketsSent:     c.recordsSent.Load(),
		BytesReceived:   c.bytesReceived.Load(),
		PacketsReceived: c.recordsReceived.Load(),
	}
}
