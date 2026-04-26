package qmux

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFlowController_AddReceivedBytes(t *testing.T) {
	tests := map[string]struct {
		initialWindow uint64
		addBytes      uint64
		wantOk        bool
	}{
		"within window": {
			initialWindow: 100,
			addBytes:      50,
			wantOk:        true,
		},
		"exactly window": {
			initialWindow: 100,
			addBytes:      100,
			wantOk:        true,
		},
		"outside window": {
			initialWindow: 100,
			addBytes:      101,
			wantOk:        false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fc := newFlowController(tt.initialWindow)
			ok := fc.AddReceivedBytes(tt.addBytes)
			assert.Equal(t, tt.wantOk, ok)
		})
	}
}

func TestFlowController_AddReadBytes(t *testing.T) {
	tests := map[string]struct {
		initialWindow uint64
		readBytes     uint64
		wantUpdate    bool
		wantLimit     uint64
	}{
		"small read - no update": {
			initialWindow: 100,
			readBytes:     10,
			wantUpdate:    false,
			wantLimit:     0,
		},
		"large read - update": {
			initialWindow: 100,
			readBytes:     60, // more than half
			wantUpdate:    true,
			wantLimit:     160, // readBytes (60) + initialWindow (100)
		},
		"exact half - update": {
			initialWindow: 100,
			readBytes:     50,
			wantUpdate:    true,
			wantLimit:     150,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fc := newFlowController(tt.initialWindow)
			// Mock receiving the bytes first
			fc.AddReceivedBytes(tt.readBytes)
			
			update, limit := fc.AddReadBytes(tt.readBytes)
			assert.Equal(t, tt.wantUpdate, update)
			assert.Equal(t, tt.wantLimit, limit)
		})
	}
}

func TestFlowController_UpdateSendWindow(t *testing.T) {
	fc := newFlowController(100)
	
	// Initial state
	assert.Equal(t, uint64(100), fc.SendWindowRemaining())
	
	// Consume some window
	fc.AddSentBytes(40)
	assert.Equal(t, uint64(60), fc.SendWindowRemaining())
	
	// Update window to higher limit
	fc.UpdateSendWindow(150)
	assert.Equal(t, uint64(110), fc.SendWindowRemaining())
	
	// Update window to lower limit (should be ignored)
	fc.UpdateSendWindow(100)
	assert.Equal(t, uint64(110), fc.SendWindowRemaining())
}
