package wire

import (
	"bytes"
	"io"
	"sync"

	"github.com/quic-go/quic-go/quicvarint"
)

var bufferPool = sync.Pool{
	New: func() any {
		b := new(bytes.Buffer)
		b.Grow(17000) // Slightly more than default max record size
		return b
	},
}

// RecordReader reads QMux records.
type RecordReader struct {
	r quicvarint.Reader
}

// NewRecordReader creates a new RecordReader.
func NewRecordReader(r io.Reader) *RecordReader {
	return &RecordReader{r: quicvarint.NewReader(r)}
}

// ReadRecord reads the next QMux record and returns the frames.
// It can optionally reuse the given slice to reduce allocations.
func (rr *RecordReader) ReadRecord(reuse []Frame) ([]Frame, error) {
	size, err := quicvarint.Read(rr.r)
	if err != nil {
		return nil, err
	}

	lr := &io.LimitedReader{R: rr.r, N: int64(size)}
	frames := reuse[:0]
	for lr.N > 0 {
		f, err := ParseFrame(lr)
		if err != nil {
			return nil, err
		}
		frames = append(frames, f)
	}
	return frames, nil
}

// RecordWriter writes QMux records.
type RecordWriter struct {
	w quicvarint.Writer
}

// NewRecordWriter creates a new RecordWriter.
func NewRecordWriter(w io.Writer) *RecordWriter {
	return &RecordWriter{w: quicvarint.NewWriter(w)}
}

// WriteRecord writes a QMux record containing the given frames.
func (rw *RecordWriter) WriteRecord(frames ...Frame) error {
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()

	for _, f := range frames {
		if err := f.Write(buf); err != nil {
			bufferPool.Put(buf)
			return err
		}
	}

	err := rw.writeBuffer(buf)
	bufferPool.Put(buf)
	return err
}

// WriteRecordSingle writes a QMux record containing a single frame, avoiding variadic allocation.
func (rw *RecordWriter) WriteRecordSingle(f Frame) error {
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()

	if err := f.Write(buf); err != nil {
		bufferPool.Put(buf)
		return err
	}

	err := rw.writeBuffer(buf)
	bufferPool.Put(buf)
	return err
}

func (rw *RecordWriter) writeBuffer(buf *bytes.Buffer) error {
	var header [8]byte
	s := quicvarint.Append(header[:0], uint64(buf.Len()))
	if _, err := rw.w.Write(s); err != nil {
		return err
	}
	_, err := rw.w.Write(buf.Bytes())
	return err
}
