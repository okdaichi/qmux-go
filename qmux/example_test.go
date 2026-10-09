package qmux_test

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/okdaichi/qmux-go/qmux"
)

func Example() {
	// A TCP echo server speaking QMux.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = ln.Close() }() // not actionable: the example is over

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		sess, err := qmux.Server(conn, nil)
		if err != nil {
			return
		}
		for {
			str, err := sess.AcceptStream(context.Background())
			if err != nil {
				return
			}
			go func() {
				if _, err := io.Copy(str, str); err != nil {
					return
				}
				if err := str.Close(); err != nil {
					return
				}
			}()
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		fmt.Println(err)
		return
	}
	sess, err := qmux.Dial(conn, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = sess.Close() }() // not actionable: the example is over

	str, err := sess.OpenStreamSync(context.Background())
	if err != nil {
		fmt.Println(err)
		return
	}
	if _, err := str.Write([]byte("hello qmux")); err != nil {
		fmt.Println(err)
		return
	}
	if err := str.Close(); err != nil {
		fmt.Println(err)
		return
	}

	reply, err := io.ReadAll(str)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(reply))

	// Output: hello qmux
}

func ExampleServerMessages() {
	// mc adapts a WebSocket connection whose negotiated subprotocol names
	// the application protocol and the QMux draft, such as "qmux-02.myapp".
	var mc qmux.MessageConn

	sess, err := qmux.ServerMessages(mc, nil)
	if err != nil {
		return
	}
	defer sess.Close()
}
