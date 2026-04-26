package qmux_test

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/okdaichi/qmux-go/qmux"
)

func Example() {
	// Setup a simple TCP echo server using QMux
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()

	go func() {
		conn, _ := ln.Accept()
		sess, _ := qmux.Server(conn, nil)
		for {
			str, err := sess.AcceptStream(context.Background())
			if err != nil {
				return
			}
			go io.Copy(str, str) // Echo
		}
	}()

	// Client side
	conn, _ := net.Dial("tcp", ln.Addr().String())
	sess, _ := qmux.Dial(conn, nil)
	str, _ := sess.OpenStream()

	str.Write([]byte("hello qmux"))
	buf := make([]byte, 10)
	n, _ := str.Read(buf)
	fmt.Println(string(buf[:n]))

	// Output: hello qmux
}

func ExampleConn_ConnectionStats() {
	// Assuming you have an active session
	var sess *qmux.Conn
	
	// Get real-time throughput and RTT metrics
	stats := sess.ConnectionStats()
	fmt.Printf("Bytes Sent: %d\n", stats.BytesSent)
	fmt.Printf("Smoothed RTT: %v\n", stats.SmoothedRTT)
}

func ExampleNewNetConn() {
	// Adapt a hypothetical message-based transport to net.Conn
	type myMessageConn struct { /* ... */ }
	// func (c *myMessageConn) ReadMessage() ([]byte, error) { ... }
	// func (c *myMessageConn) WriteMessage(p []byte) error  { ... }
	// func (c *myMessageConn) Close() error                 { ... }

	var mc qmux.MessageConn // = &myMessageConn{}
	
	// Wrap it!
	nc := qmux.NewNetConn(mc)
	
	// Now use it with QMux or any other net.Conn consumer
	sess, _ := qmux.Dial(nc, nil)
	_ = sess
}
