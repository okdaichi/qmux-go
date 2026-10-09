package qmux

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coderws "github.com/coder/websocket"
	gorillaws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gorillaConn adapts a gorilla/websocket connection to MessageConn.
type gorillaConn struct {
	*gorillaws.Conn
}

var _ MessageConn = gorillaConn{}

func (c gorillaConn) ReadMessage() ([]byte, error) {
	_, p, err := c.Conn.ReadMessage()
	return p, err
}

func (c gorillaConn) WriteMessage(p []byte) error {
	return c.Conn.WriteMessage(gorillaws.BinaryMessage, p)
}

func TestServer_WebSocket(t *testing.T) {
	tests := map[string]struct {
		serve func(w http.ResponseWriter, r *http.Request) (*Conn, error)
		dial  func(t *testing.T, ctx context.Context, url string) (*Conn, error)
	}{
		"gorilla, one record per message": {
			serve: func(w http.ResponseWriter, r *http.Request) (*Conn, error) {
				ws, err := (&gorillaws.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return nil, err
				}
				return ServerMessages(gorillaConn{ws}, nil)
			},
			dial: func(t *testing.T, _ context.Context, url string) (*Conn, error) {
				ws, _, err := gorillaws.DefaultDialer.Dial(url, nil)
				require.NoError(t, err)
				return DialMessages(gorillaConn{ws}, nil)
			},
		},
		"gorilla, as a byte stream": {
			serve: func(w http.ResponseWriter, r *http.Request) (*Conn, error) {
				ws, err := (&gorillaws.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return nil, err
				}
				return Server(NetConn(gorillaConn{ws}), nil)
			},
			dial: func(t *testing.T, _ context.Context, url string) (*Conn, error) {
				ws, _, err := gorillaws.DefaultDialer.Dial(url, nil)
				require.NoError(t, err)
				return Dial(NetConn(gorillaConn{ws}), nil)
			},
		},
		"coder, as a byte stream": {
			serve: func(w http.ResponseWriter, r *http.Request) (*Conn, error) {
				ws, err := coderws.Accept(w, r, nil)
				if err != nil {
					return nil, err
				}
				// The connection outlives the request's context.
				ctx := context.WithoutCancel(r.Context())
				return Server(coderws.NetConn(ctx, ws, coderws.MessageBinary), nil)
			},
			dial: func(t *testing.T, ctx context.Context, url string) (*Conn, error) {
				ws, _, err := coderws.Dial(ctx, url, nil)
				require.NoError(t, err)
				return Dial(coderws.NetConn(ctx, ws, coderws.MessageBinary), nil)
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := testContext(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				server, err := tt.serve(w, r)
				if err != nil {
					return
				}
				defer func() { assert.NoError(t, server.Close()) }()
				s, err := server.AcceptStream(ctx)
				if err != nil {
					return
				}
				if _, err := io.Copy(s, s); err != nil {
					return
				}
				assert.NoError(t, s.Close())
				<-server.Context().Done()
			}))
			defer srv.Close()

			client, err := tt.dial(t, ctx, "ws"+strings.TrimPrefix(srv.URL, "http"))
			require.NoError(t, err)
			defer func() { assert.NoError(t, client.Close()) }()

			s, err := client.OpenStreamSync(ctx)
			require.NoError(t, err)
			// More than one record's worth.
			msg := []byte(strings.Repeat("hello over websocket ", 2000))
			go func() {
				if _, err := s.Write(msg); err != nil {
					return
				}
				assert.NoError(t, s.Close())
			}()
			got, err := io.ReadAll(s)
			require.NoError(t, err)
			assert.Equal(t, msg, got)
		})
	}
}
