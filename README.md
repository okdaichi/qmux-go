# qmux-go

[![Go Reference](https://pkg.go.dev/badge/github.com/okdaichi/qmux-go.svg)](https://pkg.go.dev/github.com/okdaichi/qmux-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/okdaichi/qmux-go)](https://goreportcard.com/report/github.com/okdaichi/qmux-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

`qmux-go` is a Go implementation of the **QMux protocol**, providing QUIC-like stream and datagram multiplexing semantics over any reliable, bi-directional byte stream transport, such as TCP, TLS, or WebSockets.

It follows **[draft-ietf-quic-qmux-02](https://www.ietf.org/archive/id/draft-ietf-quic-qmux-02.html)**, and interoperates with [`@moq/qmux`](https://www.npmjs.com/package/@moq/qmux) over WebSocket.

## Features

- **Streams**: bidirectional and unidirectional, with QUIC's stream limits, flow control, resets, deadlines and priorities (RFC 9218).
- **Datagrams**: the `DATAGRAM` extension (RFC 9221), delivered reliably and in order as QMux specifies.
- **Transports**: any `net.Conn` (TCP, TLS, Unix sockets), or a message transport such as WebSocket with one record per message (`DialMessages`, `ServerMessages`).
- **quic-go shaped API**: `Conn` and its streams mirror quic-go's, and report errors with its error types.

QMux does not negotiate the application protocol: the transport does, with ALPN over TLS or the subprotocol over WebSocket (for example `qmux-02.myapp`).

Not implemented: `RESET_STREAM_AT`, 0-RTT, and flow control window auto-tuning.

## Installation

```bash
go get github.com/okdaichi/qmux-go
```

## Documentation & Examples

For detailed API documentation and runnable examples, please visit the **[Go Reference](https://pkg.go.dev/github.com/okdaichi/qmux-go/qmux)**. 

## License

MIT
