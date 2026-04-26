package qmux

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/okdaichi/qmux-go/qmux/internal/wire"
	"github.com/quic-go/quic-go/quicvarint"
)

type discardByteWriter struct{}

func (d discardByteWriter) Write(p []byte) (int, error) { return len(p), nil }
func (d discardByteWriter) WriteByte(c byte) error      { return nil }

func BenchmarkVarInt_Write(b *testing.B) {
	v := uint64(0x3fffffffffffffff)
	var buf [8]byte
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = quicvarint.Append(buf[:0], v)
	}
}

func BenchmarkVarInt_Read(b *testing.B) {
	data := []byte{0xc0, 0, 0, 0, 0, 0, 0, 0x35}
	br := bytes.NewReader(data)
	qr := quicvarint.NewReader(br)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = quicvarint.Read(qr)
		br.Reset(data)
	}
}

func BenchmarkRecord_Write(b *testing.B) {
	w := wire.NewRecordWriter(discardByteWriter{})
	f := wire.GetStreamFrame()
	f.StreamID = 1
	f.Data = make([]byte, 1024)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = w.WriteRecordSingle(f)
	}
}

func BenchmarkStream_Throughput(b *testing.B) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer ln.Close()

	serverDone := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		server, err := Server(conn, nil)
		if err != nil {
			return
		}
		defer server.Close()
		
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		
		str, err := server.AcceptStream(ctx)
		if err != nil {
			return
		}
		io.Copy(io.Discard, str)
		close(serverDone)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	
	client, err := Dial(conn, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close()
	
	str, err := client.OpenStream()
	if err != nil {
		b.Fatal(err)
	}

	data := make([]byte, 32*1024)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := str.Write(data)
		if err != nil {
			b.Fatal(err)
		}
	}
	str.Close()
	
	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		b.Fatal("server timed out")
	}
}
