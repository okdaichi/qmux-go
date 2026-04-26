# Changelog

All notable changes to this project will be documented in this file.

## [0.2.0] - 2024-04-26

### Refactored & Standardized
- **API Naming**: Renamed `NewNetConn` to `NetConn` to follow Go idiomatic patterns.
- **Datagram API**: Renamed `SendMessage`/`ReceiveMessage` to `SendDatagram`/`ReceiveDatagram` for consistency with the `quic-go` library.
- **Error Handling**: 
    - Replaced the custom Error struct with standard `quic.TransportError` and `quic.ApplicationError`.
    - Introduced local aliases for `ApplicationErrorCode` and `StreamErrorCode` for a more integrated API.
- **Observability**: Enhanced `ConnectionState` to report actual TLS state from underlying transports and accurate datagram support status.

### Added
- **Documentation**: Added comprehensive package-level documentation in `doc.go`.
- **Runnable Examples**: Added standard Go `Example` functions in `example_test.go` covering TCP and generic transport adaptation.
- **Mage Targets**: Enhanced build system with `Bench`, `Coverage`, and `Tidy` targets.

### Optimized
- **QuicVarInt Integration**: Replaced custom varint implementation with the highly optimized `github.com/quic-go/quic-go/quicvarint`.
- **Zero-Allocation Path**: Optimized `writeVarInt` to achieve zero allocations when writing to buffers.
- **Buffered I/O**: Integrated `bufio` to batch transport-level writes and improve throughput.

## [0.1.0] - 2024-04-26

### Initial Release
- **QMux Protocol Implementation**: Complete implementation of the QMux protocol following **draft-ietf-quic-qmux-01**.
- **Core Features**: Bi-directional/unidirectional streams, flow control, and unreliable datagrams.
