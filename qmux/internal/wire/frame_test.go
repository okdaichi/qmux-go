package wire

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	tests := map[string]struct {
		frame Frame
	}{
		"reset stream":          {frame: &ResetStream{StreamID: 6, Code: 42, FinalSize: 1 << 20}},
		"stop sending":          {frame: &StopSending{StreamID: 4, Code: 7}},
		"stream":                {frame: &Stream{StreamID: 2, Data: []byte("hello")}},
		"stream with offset":    {frame: &Stream{StreamID: 2, Offset: 70000, Data: []byte("hello")}},
		"stream fin, no data":   {frame: &Stream{StreamID: 2, Offset: 5, Data: []byte{}, Fin: true}},
		"max data":              {frame: &MaxData{Max: 1 << 30}},
		"max stream data":       {frame: &MaxStreamData{StreamID: 8, Max: 1 << 16}},
		"max streams bidi":      {frame: &MaxStreams{Max: 200}},
		"max streams uni":       {frame: &MaxStreams{Uni: true, Max: 200}},
		"transport close":       {frame: &ConnectionClose{Code: ProtocolViolation, FrameType: 8, Reason: "bad"}},
		"application close":     {frame: &ConnectionClose{Application: true, Code: 3, Reason: "bye"}},
		"datagram":              {frame: &Datagram{Data: []byte("dgram")}},
		"ping request":          {frame: &Ping{Sequence: 9}},
		"ping response":         {frame: &Ping{Response: true, Sequence: 9}},
		"transport parameters":  {frame: &TransportParameters{Parameters: Parameters{InitialMaxData: 1000, MaxRecordSize: 20000}}},
		"empty parameters":      {frame: &TransportParameters{Parameters: Parameters{MaxRecordSize: DefaultMaxRecordSize}}},
		"all parameters":        {frame: &TransportParameters{Parameters: Parameters{1, 2, 3, 4, 5, 6, 7, 8, 16382}}},
		"max streams at 2^60":   {frame: &MaxStreams{Max: 1 << 60}},
		"stream, largest id":    {frame: &Stream{StreamID: 1<<62 - 1, Data: []byte{0}}},
		"datagram, empty":       {frame: &Datagram{Data: []byte{}}},
		"application close, ''": {frame: &ConnectionClose{Application: true}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			// A frame after it shows that the first is delimited.
			b := (&MaxData{Max: 1}).Append(tt.frame.Append(nil))

			got, rest, err := Parse(b)
			require.NoError(t, err)
			assert.Equal(t, tt.frame, got)

			next, rest, err := Parse(rest)
			require.NoError(t, err)
			assert.Equal(t, &MaxData{Max: 1}, next)
			assert.Empty(t, rest)
		})
	}
}

func TestParse_WithoutLength(t *testing.T) {
	tests := map[string]struct {
		input    []byte
		expected any
	}{
		"stream without length": {
			input:    []byte{0x08, 0x02, 'a', 'b', 'c'},
			expected: &Stream{StreamID: 2, Data: []byte("abc")},
		},
		"stream with offset and fin, without length": {
			input:    []byte{0x0d, 0x02, 0x05, 'a'},
			expected: &Stream{StreamID: 2, Offset: 5, Data: []byte("a"), Fin: true},
		},
		"datagram without length": {
			input:    []byte{0x30, 'a', 'b'},
			expected: &Datagram{Data: []byte("ab")},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, rest, err := Parse(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
			assert.Empty(t, rest)
		})
	}
}

func TestParse_Padding(t *testing.T) {
	f, rest, err := Parse([]byte{0x00, 0x00, 0x10, 0x05, 0x00})
	require.NoError(t, err)
	assert.Equal(t, &MaxData{Max: 5}, f)

	f, rest, err = Parse(rest)
	require.NoError(t, err)
	assert.Nil(t, f)
	assert.Empty(t, rest)
}

func TestParse_Blocked(t *testing.T) {
	tests := map[string]struct {
		input []byte
	}{
		"data blocked":        {input: []byte{0x14, 0x05}},
		"stream data blocked": {input: []byte{0x15, 0x04, 0x05}},
		"streams blocked":     {input: []byte{0x16, 0x05}},
		"streams blocked uni": {input: []byte{0x17, 0x05}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			f, rest, err := Parse(tt.input)
			require.NoError(t, err)
			assert.Equal(t, &Blocked{}, f)
			assert.Empty(t, rest)
		})
	}
}

func TestParse_Error(t *testing.T) {
	tests := map[string]struct {
		input []byte
		code  uint64
	}{
		"PING is prohibited":              {input: []byte{0x01}, code: FrameEncodingError},
		"ACK is prohibited":               {input: []byte{0x02, 0, 0, 0, 0}, code: FrameEncodingError},
		"CRYPTO is prohibited":            {input: []byte{0x06, 0, 0}, code: FrameEncodingError},
		"NEW_TOKEN is prohibited":         {input: []byte{0x07, 0}, code: FrameEncodingError},
		"HANDSHAKE_DONE is prohibited":    {input: []byte{0x1e}, code: FrameEncodingError},
		"PATH_CHALLENGE is prohibited":    {input: []byte{0x1a, 0, 0, 0, 0, 0, 0, 0, 0}, code: FrameEncodingError},
		"unknown frame type":              {input: []byte{0x40, 0x99}, code: FrameEncodingError},
		"truncated type":                  {input: []byte{0x40}, code: FrameEncodingError},
		"truncated reset stream":          {input: []byte{0x04, 0x02}, code: FrameEncodingError},
		"stream data shorter than length": {input: []byte{0x0a, 0x02, 0x05, 'a'}, code: FrameEncodingError},
		"close reason shorter":            {input: []byte{0x1d, 0x00, 0x05, 'a'}, code: FrameEncodingError},
		"max streams above 2^60":          {input: (&MaxStreams{Max: 1<<60 + 1}).Append(nil), code: FrameEncodingError},
		"parameters shorter than length":  {input: append((&TransportParameters{}).Append(nil)[:8], 0x05, 0x04), code: FrameEncodingError},
		"prohibited parameter":            {input: parametersFrame(0x0e, 0x01, 0x02), code: TransportParameterError},
		"repeated parameter":              {input: parametersFrame(0x04, 0x01, 0x02, 0x04, 0x01, 0x02), code: TransportParameterError},
		"parameter value not an integer":  {input: parametersFrame(0x04, 0x02, 0x02, 0x02), code: TransportParameterError},
		"parameter value truncated":       {input: parametersFrame(0x04, 0x05, 0x02), code: TransportParameterError},
		"max_record_size below default": {
			input: (&TransportParameters{Parameters: Parameters{MaxRecordSize: DefaultMaxRecordSize - 1}}).Append(nil),
			code:  TransportParameterError,
		},
		"initial_max_streams above 2^60": {
			input: (&TransportParameters{Parameters: Parameters{InitialMaxStreamsUni: 1<<60 + 1}}).Append(nil),
			code:  TransportParameterError,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := Parse(tt.input)
			var werr *Error
			require.ErrorAs(t, err, &werr)
			assert.Equal(t, tt.code, werr.Code)
		})
	}
}

func TestParse_UnknownParameter(t *testing.T) {
	// A parameter from the range reserved for exercising this (31*N+27),
	// with an arbitrary value.
	f, _, err := Parse(parametersFrame(0x1b, 0x03, 0xff, 0xff, 0xff, 0x04, 0x01, 0x09))
	require.NoError(t, err)
	assert.Equal(t, &TransportParameters{Parameters: Parameters{InitialMaxData: 9, MaxRecordSize: DefaultMaxRecordSize}}, f)
}

func TestTransportParameters_Append(t *testing.T) {
	// "\xffQS0\r\n\r\n" on the wire tells QMux from HTTP, then the length.
	b := (&TransportParameters{}).Append(nil)
	assert.Equal(t, []byte{0xff, 0x51, 0x53, 0x30, 0x0d, 0x0a, 0x0d, 0x0a, 0x00}, b)
}

func TestPing_Append(t *testing.T) {
	// The sequence number is a variable-length integer.
	b := (&Ping{Sequence: 3}).Append(nil)
	assert.Equal(t, []byte{0xf4, 0x8c, 0x67, 0x52, 0x9e, 0xf8, 0xc7, 0xbd, 0x03}, b)
}

// parametersFrame returns a QX_TRANSPORT_PARAMETERS frame with the given
// bytes as its parameters.
func parametersFrame(params ...byte) []byte {
	b := (&TransportParameters{}).Append(nil)[:8]
	b = append(b, byte(len(params)))
	return append(b, params...)
}
