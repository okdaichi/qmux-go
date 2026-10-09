package qmux

import (
	"io"
	"sync"
)

var _ MessageConn = (*fakeMessageConn)(nil)

// fakeMessageConn is one end of an in-memory message transport. The test
// plays the peer through in and out. The zero value reads io.EOF and
// discards what is written.
type fakeMessageConn struct {
	in  chan []byte // messages for ReadMessage; nil reads io.EOF
	out chan []byte // messages written by WriteMessage; nil discards them

	once   sync.Once
	closed chan struct{}
}

// done returns the channel that Close closes.
func (f *fakeMessageConn) done() chan struct{} {
	f.once.Do(func() { f.closed = make(chan struct{}) })
	return f.closed
}

func (f *fakeMessageConn) ReadMessage() ([]byte, error) {
	if f.in == nil {
		return nil, io.EOF
	}
	select {
	case p := <-f.in:
		return p, nil
	case <-f.done():
		return nil, io.EOF
	}
}

func (f *fakeMessageConn) WriteMessage(p []byte) error {
	if f.out == nil {
		return nil
	}
	// The caller reuses p: keep a copy, as the interface requires.
	select {
	case f.out <- append([]byte(nil), p...):
		return nil
	case <-f.done():
		return io.ErrClosedPipe
	}
}

func (f *fakeMessageConn) Close() error {
	select {
	case <-f.done():
	default:
		close(f.done())
	}
	return nil
}
