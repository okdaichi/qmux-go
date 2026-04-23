# qmux-go

`qmux-go` is a minimal scaffold for a future Go project related to **qmux**.

This repository does NOT implement qmux yet.

Specification reference: [draft-ietf-quic-qmux-01](https://www.ietf.org/archive/id/draft-ietf-quic-qmux-01.html)

## What is qmux

At a high level, qmux is an approach to multiplexing streams over an underlying transport.

## Project status (scaffold only)

This repository currently provides only a compile-able project skeleton.

## Goals

- Keep a clean and minimal repository structure
- Provide a starting point for future implementation work
- Ensure basic `go build ./...` and `go test ./...` workflows pass

## Non-goals

- No protocol implementation
- No detailed API yet
