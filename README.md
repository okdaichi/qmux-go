# qmux-go

[![Go Reference](https://pkg.go.dev/badge/github.com/okdaichi/qmux-go.svg)](https://pkg.go.dev/github.com/okdaichi/qmux-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/okdaichi/qmux-go)](https://goreportcard.com/report/github.com/okdaichi/qmux-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

`qmux-go` is a high-performance, feature-complete implementation of the **QMux protocol** in Go. It provides QUIC-like stream and datagram multiplexing semantics over any reliable, bi-directional byte stream transport, such as TCP, TLS, or WebSockets.

Following the **[draft-ietf-quic-qmux-01](https://www.ietf.org/archive/id/draft-ietf-quic-qmux-01.html)** specification, `qmux-go` allows developers to leverage the power of QUIC's multiplexing features even when restricted to non-UDP transports.

## Features

- **Full Specification Compliance**: Implements all mandatory and optional features of QMux draft-01.
- **Multiplexing Engine**: Supports bi-directional and uni-directional streams with QUIC-style stream ID management.
- **Flow Control**: Sophisticated connection-level and stream-level flow control inspired by `quic-go`.
- **Unreliable Datagrams**: Supports the `DATAGRAM` extension (RFC 9221) for low-latency, non-reliable messaging.
- **Transport Agnostic**: Works over raw `net.Conn` and provides a generic `MessageConn` adapter for WebSockets (Gorilla, Coder, etc.).
- **In-band Negotiation**: Handshake-based negotiation of transport parameters and application-level protocols (ALPN-like).
- **High Performance**: Optimized, low-allocation architecture using `sync/atomic`, buffer pooling, and `quicvarint`.
- **Rich Observability**: Detailed connection statistics (RTT, throughput) following the `quic-go` standard.

## Installation

```bash
go get github.com/okdaichi/qmux-go
```

## Quick Start (TCP)

### Server
```go
ln, _ := net.Listen("tcp", "localhost:8080")
conn, _ := ln.Accept()

// Start a QMux session
session, _ := qmux.Server(conn, nil)

// Accept streams
stream, _ := session.AcceptStream(context.Background())
io.Copy(stream, stream) // Echo
```

### Client
```go
conn, _ := net.Dial("tcp", "localhost:8080")

// Dial a QMux session
session, _ := qmux.Dial(conn, nil)

// Open a stream
stream, _ := session.OpenStream()
stream.Write([]byte("hello qmux"))
```

## WebSocket Usage

`qmux-go` provides a generic adapter to run over message-based transports like WebSockets.

### With `github.com/gorilla/websocket`

```go
// Define a simple adapter
type myAdapter struct { *websocket.Conn }
func (a *myAdapter) ReadMessage() ([]byte, error) { _, p, err := a.Conn.ReadMessage(); return p, err }
func (a *myAdapter) WriteMessage(p []byte) error  { return a.WriteMessage(websocket.BinaryMessage, p) }

// Wrap and use
nc := qmux.NewNetConn(&myAdapter{conn})
session, _ := qmux.Dial(nc, nil)
```

### With `github.com/coder/websocket`

```go
nc := websocket.NetConn(ctx, conn, websocket.MessageBinary)
session, _ := qmux.Dial(nc, nil)
```

## Performance

`qmux-go` is designed for high-throughput scenarios. Micro-benchmarks show near-zero allocations in the protocol's hot paths.

| Benchmark | Throughput | Allocations |
|-----------|------------|-------------|
| **VarInt Encoding** | ~6 ns/op | 0 B/op |
| **Record Writing** | ~50 ns/op | 0 B/op |
| **Stream Throughput** | ~1,000 MB/s | < 50 allocs/op |

*Benchmarks measured on Intel i7-10700K over local transport.*

## Specification Compliance

This library strictly adheres to `draft-ietf-quic-qmux-01`. Key enforced behaviors include:
- Strict `STREAM` frame offset ordering for zero-copy efficiency.
- Mandatory `QX_TRANSPORT_PARAMETERS` exchange.
- Immediate connection closure without a draining period.
- Rejection of prohibited QUIC frames (e.g., `ACK`, `CRYPTO`).

## License

MIT
