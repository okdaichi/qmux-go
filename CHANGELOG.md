# Changelog

All notable changes to this project will be documented in this file.

## [0.1.0] - 2024-04-26

### Initial Release
- **QMux Protocol Implementation**: This is the first version of `qmux-go`, providing a complete implementation of the QMux protocol.
- **Specification Compliance**: Fully follows the **draft-ietf-quic-qmux-01** specification, enabling QUIC-like stream and datagram multiplexing over reliable, bi-directional byte streams (such as TCP or WebSockets).
- **Core Features**:
    - Bi-directional and uni-directional stream multiplexing.
    - Connection and stream-level flow control.
    - Unreliable datagram support (RFC 9221).
    - In-band transport parameter negotiation and handshake.
    - Transport-agnostic design with implicit support for standard and third-party WebSocket libraries.
    - High-performance, low-allocation architecture inspired by `quic-go`.
