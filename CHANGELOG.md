# Changelog

All notable changes to this project will be documented in this file.

## [0.3.0] - 2026-10-10

The connection, stream and wire code is rewritten against **draft-ietf-quic-qmux-02**. The wire format changes, so this version does not talk to 0.2.0.

### Fixed
- **Stream limits**: `MAX_STREAMS` was never sent, so a connection could open 100 streams of each kind over its lifetime and then stalled. The limit now moves as streams finish.
- **`Write` retained the caller's buffer** until a background loop sent it: reusing the buffer corrupted the data. `Write` now copies into the record before it returns.
- **Finished streams were never forgotten**, growing memory for the life of the connection.
- **The peer's `initial_max_stream_data_*` parameters were ignored**; the local window was used as the send limit instead.
- **Wire format**: `QX_TRANSPORT_PARAMETERS` lacked its `Length` field, `max_record_size` used the wrong identifier, and `QX_PING` carried eight fixed bytes instead of a variable-length sequence number.
- `STREAM` and `DATAGRAM` frames without a length are accepted; they run to the end of the record.
- Violations by the peer close the connection with a transport `CONNECTION_CLOSE` and the error code the draft names. They surface as `*quic.TransportError`, and an idle timeout as `*quic.IdleTimeoutError`.
- Stream limits, flow control limits, final sizes and record sizes are enforced on what the peer sends.
- Deadlines return `os.ErrDeadlineExceeded`.
- The idle timeout is the shorter of the two endpoints' values, and closes without sending a frame.
- CI: the workflows were in `.github/workflow/` and never ran.
- A stream's read buffer stays within its flow control window however the application reads.
- With `Config.KeepAlivePeriod` set, pings left unanswered for the idle timeout close the connection. Sending them reset the idle timer, so a peer that was gone kept the connection open.

### Added
- `DialMessages` and `ServerMessages`: one record per message, without the `Size` field. This is the WebSocket mapping of `@moq/qmux`, which the implementation is tested against.
- `SendStream.Context` and `Stream.Context`, cancelled when the sending side ends.
- `ErrDatagramsNotSupported`.
- `SetPriority` on `SendStream` and `Stream`, with the urgency and incremental parameters of RFC 9218 and quic-go's defaults. When several streams wait for the transport, the most urgent writes the next record; control frames go ahead of all stream data.
- `Peek` on `ReceiveStream` and `Stream`.
- `Config.HandshakeIdleTimeout` (5 seconds by default): `Dial` and `Server` fail with a `*quic.HandshakeTimeoutError` when the peer never sends its transport parameters.
- `Config.Clone`.

### Changed
- **Breaking**: `Config.ApplicationProtocols` is removed, with the private transport parameter behind it. The draft leaves protocol negotiation to the transport (ALPN, or the WebSocket subprotocol) and forbids undeclared parameters.
- **Breaking**: `Dial`, `Server`, `DialMessages` and `ServerMessages` take a `context.Context` and return once the peer's transport parameters are in, as `quic.Dial` returns once the handshake is done. A peer that fails the handshake fails the call.
- **Breaking**: the `TransportErrorCode` constants (`InternalError`, `ProtocolViolationError`, ...) are removed. quic-go exports the same codes: `quic.InternalError`, `quic.ProtocolViolation` and so on.
- **Breaking**: `OpenStream` and `OpenUniStream` no longer wait at the stream limit; they return a `quic.StreamLimitReachedError`, as quic-go does. Use the `Sync` variants to wait.
- **Breaking**: a stream reaches the peer with the first frame sent on it, as in QUIC, not when it is opened.
- `Stream.Close` ends the sending side only.
- `SendDatagram` returns a `*quic.DatagramTooLargeError` for a datagram larger than the peer accepts.
- `Config.KeepAlivePeriod` is at most half of `MaxIdleTimeout`.
- quic-go is required at v0.63.0.
- Zero fields of `Config` take their defaults. The default windows are 512 KiB per stream and 1 MiB per connection.
- `Write` and `Close` write to the transport directly, so a slow peer applies backpressure instead of growing a queue.
- `DATA_BLOCKED`, `STREAM_DATA_BLOCKED` and `STREAMS_BLOCKED` frames are accepted but no longer sent.

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
