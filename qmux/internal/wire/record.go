package wire

import (
	"bytes"
	"io"
)

// RecordReader reads QMux records.
type RecordReader struct {
	r io.Reader
}

// NewRecordReader creates a new RecordReader.
func NewRecordReader(r io.Reader) *RecordReader {
	return &RecordReader{r: r}
}

// ReadRecord reads the next QMux record and returns the frames.
func (rr *RecordReader) ReadRecord() ([]Frame, error) {
	size, err := ReadVarInt(rr.r)
	if err != nil {
		return nil, err
	}

	lr := &io.LimitedReader{R: rr.r, N: int64(size)}
	var frames []Frame
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
	w io.Writer
}

// NewRecordWriter creates a new RecordWriter.
func NewRecordWriter(w io.Writer) *RecordWriter {
	return &RecordWriter{w: w}
}

// WriteRecord writes a QMux record containing the given frames.
func (rw *RecordWriter) WriteRecord(frames ...Frame) error {
	var buf bytes.Buffer
	for _, f := range frames {
		if err := f.Write(&buf); err != nil {
			return err
		}
	}

	if err := WriteVarInt(rw.w, uint64(buf.Len())); err != nil {
		return err
	}
	_, err := rw.w.Write(buf.Bytes())
	return err
}
