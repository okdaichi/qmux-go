package qmux

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	gorillaws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"
)

// TestCoderWebSocketTransport tests QMux over github.com/coder/websocket
func TestCoderWebSocketTransport(t *testing.T) {
	serverDone := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(coderws.StatusInternalError, "internal error")

		nc := coderws.NetConn(context.Background(), c, coderws.MessageBinary)
		defer nc.Close()

		qServer, err := Server(nc, nil)
		if err != nil {
			return
		}
		defer qServer.Close()

		str, err := qServer.AcceptStream(context.Background())
		if err != nil {
			return
		}
		defer str.Close()

		buf := make([]byte, 1024)
		n, _ := str.Read(buf)
		_, _ = str.Write(buf[:n])

		// Wait for client signal before closing
		<-serverDone
	}))
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	c, _, err := coderws.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	defer c.Close(coderws.StatusNormalClosure, "")

	ncClient := coderws.NetConn(ctx, c, coderws.MessageBinary)
	defer ncClient.Close()

	qClient, err := Dial(ncClient, nil)
	require.NoError(t, err)
	defer qClient.Close()

	str, err := qClient.OpenStream()
	require.NoError(t, err)
	defer str.Close()

	msg := "hello from coder/websocket"
	_, err = str.Write([]byte(msg))
	assert.NoError(t, err)

	buf := make([]byte, 1024)
	n, err := str.Read(buf)
	assert.NoError(t, err)
	assert.Equal(t, msg, string(buf[:n]))

	close(serverDone)
}

// gorillaAdapter implements qmux.MessageConn for gorilla/websocket
type gorillaAdapter struct {
	*gorillaws.Conn
}

func (a *gorillaAdapter) ReadMessage() ([]byte, error) {
	_, p, err := a.Conn.ReadMessage()
	return p, err
}

func (a *gorillaAdapter) WriteMessage(p []byte) error {
	return a.Conn.WriteMessage(gorillaws.BinaryMessage, p)
}

func TestGorillaWebSocketTransport(t *testing.T) {
	upgrader := gorillaws.Upgrader{}
	serverDone := make(chan struct{})

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		// Use the generic adapter
		nc := NetConn(&gorillaAdapter{Conn: c})
		qServer, err := Server(nc, nil)
		if err != nil {
			return
		}
		defer qServer.Close()

		str, err := qServer.AcceptStream(context.Background())
		if err != nil {
			return
		}
		defer str.Close()

		buf := make([]byte, 1024)
		n, _ := str.Read(buf)
		_, _ = str.Write(buf[:n])

		// Wait for client signal before closing
		<-serverDone
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	c, _, err := gorillaws.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer c.Close()

	// Use the generic adapter
	ncClient := NetConn(&gorillaAdapter{Conn: c})
	qClient, err := Dial(ncClient, nil)
	require.NoError(t, err)
	defer qClient.Close()

	str, err := qClient.OpenStream()
	require.NoError(t, err)
	defer str.Close()

	msg := "hello from gorilla/websocket"
	_, err = str.Write([]byte(msg))
	assert.NoError(t, err)

	buf := make([]byte, 1024)
	n, err := str.Read(buf)
	assert.NoError(t, err)
	assert.Equal(t, msg, string(buf[:n]))

	close(serverDone)
}

func TestXNetWebSocketTransport(t *testing.T) {
	serverDone := make(chan struct{})
	s := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		ws.PayloadType = websocket.BinaryFrame
		
		qServer, err := Server(ws, nil)
		if err != nil {
			return
		}
		defer qServer.Close()

		str, err := qServer.AcceptStream(context.Background())
		if err != nil {
			return
		}
		defer str.Close()

		buf := make([]byte, 1024)
		n, _ := str.Read(buf)
		_, _ = str.Write(buf[:n])

		<-serverDone
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	ws, err := websocket.Dial(wsURL, "", s.URL)
	require.NoError(t, err)
	defer ws.Close()
	ws.PayloadType = websocket.BinaryFrame

	qClient, err := Dial(ws, nil)
	require.NoError(t, err)
	defer qClient.Close()

	str, err := qClient.OpenStream()
	require.NoError(t, err)
	defer str.Close()

	msg := "hello from x/net/websocket"
	_, err = str.Write([]byte(msg))
	assert.NoError(t, err)

	buf := make([]byte, 1024)
	n, err := str.Read(buf)
	assert.NoError(t, err)
	assert.Equal(t, msg, string(buf[:n]))

	close(serverDone)
}
