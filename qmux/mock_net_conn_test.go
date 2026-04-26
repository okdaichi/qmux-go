package qmux

import (
	"net"
	"time"
)

var _ net.Conn = (*MockNetConn)(nil)

// MockNetConn is a simple Func-field stub for net.Conn.
type MockNetConn struct {
	ReadFunc         func(b []byte) (n int, err error)
	WriteFunc        func(b []byte) (n int, err error)
	CloseFunc        func() error
	LocalAddrFunc    func() net.Addr
	RemoteAddrFunc   func() net.Addr
	SetDeadlineFunc  func(t time.Time) error
	SetReadDeadlineFunc  func(t time.Time) error
	SetWriteDeadlineFunc func(t time.Time) error
}

func (m *MockNetConn) Read(b []byte) (n int, err error) {
	if m.ReadFunc != nil {
		return m.ReadFunc(b)
	}
	return 0, nil
}

func (m *MockNetConn) Write(b []byte) (n int, err error) {
	if m.WriteFunc != nil {
		return m.WriteFunc(b)
	}
	return len(b), nil
}

func (m *MockNetConn) Close() error {
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	return nil
}

func (m *MockNetConn) LocalAddr() net.Addr {
	if m.LocalAddrFunc != nil {
		return m.LocalAddrFunc()
	}
	return &net.TCPAddr{}
}

func (m *MockNetConn) RemoteAddr() net.Addr {
	if m.RemoteAddrFunc != nil {
		return m.RemoteAddrFunc()
	}
	return &net.TCPAddr{}
}

func (m *MockNetConn) SetDeadline(t time.Time) error {
	if m.SetDeadlineFunc != nil {
		return m.SetDeadlineFunc(t)
	}
	return nil
}

func (m *MockNetConn) SetReadDeadline(t time.Time) error {
	if m.SetReadDeadlineFunc != nil {
		return m.SetReadDeadlineFunc(t)
	}
	return nil
}

func (m *MockNetConn) SetWriteDeadline(t time.Time) error {
	if m.SetWriteDeadlineFunc != nil {
		return m.SetWriteDeadlineFunc(t)
	}
	return nil
}
