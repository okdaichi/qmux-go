# qmux-go

[![Go Reference](https://pkg.go.dev/badge/github.com/okdaichi/qmux-go.svg)](https://pkg.go.dev/github.com/okdaichi/qmux-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/okdaichi/qmux-go)](https://goreportcard.com/report/github.com/okdaichi/qmux-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

`qmux-go` is a Go implementation of the **QMux protocol**, providing QUIC-like stream and datagram multiplexing semantics over any reliable, bi-directional byte stream transport, such as TCP, TLS, or WebSockets.

Following the **[draft-ietf-quic-qmux-01](https://www.ietf.org/archive/id/draft-ietf-quic-qmux-01.html)** specification, `qmux-go` allows developers to leverage the power of QUIC's multiplexing features even when restricted to non-UDP transports.

## Features

- **Full Specification Compliance**: Implements all mandatory and optional features of QMux draft-01.
- **Multiplexing Engine**: Supports bi-directional and uni-directional streams with QUIC-style stream ID management.
- **Flow Control**: Sophisticated connection-level and stream-level flow control.
- **Unreliable Datagrams**: Supports the `DATAGRAM` extension (RFC 9221).
- **Transport Agnostic**: Works over raw `net.Conn` and provides a generic `MessageConn` adapter for WebSockets.
- **In-band Negotiation**: Handshake-based negotiation of transport parameters and application-level protocols.

## Installation

```bash
go get github.com/okdaichi/qmux-go
```

## Documentation & Examples

For detailed API documentation and runnable examples, please visit the **[Go Reference](https://pkg.go.dev/github.com/okdaichi/qmux-go/qmux)**. 

Code samples for common scenarios (TCP, TLS, and WebSockets) can be found in the [examples/](examples/) directory.

## License

MIT
